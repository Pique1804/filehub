// Full-text filename search via SQLite FTS5 (trigram tokenizer => substring match).
// Pure-Go driver (modernc.org/sqlite) keeps the single static binary.
package main

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"modernc.org/sqlite"
)

var (
	db       *sql.DB
	indexMu  sync.Mutex
	indexing bool
	indexedN int
)

// SQLite's lower() only folds ASCII. Keep case-insensitive filename sorting
// for Cyrillic and other scripts without changing the FTS index.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("filehub_lower", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			name, _ := args[0].(string)
			return strings.ToLower(name), nil
		})
}

type SearchRes struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Owner   string `json:"owner,omitempty"`
}

func initSearch(dbPath string) error {
	var err error
	db, err = sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?"+url.Values{"_txlock": {"immediate"}, "_pragma": {"busy_timeout(5000)", "synchronous(NORMAL)", "wal_autocheckpoint(1000)", "journal_size_limit(67108864)"}}.Encode())
	if err != nil {
		return err
	}
	for _, p := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		// Without this, a concurrent writer returns SQLITE_BUSY immediately and the
		// fire-and-forget db.Exec writes (favorites, album membership) are silently
		// dropped. Wait up to 5s for the lock instead.
		"PRAGMA busy_timeout=5000;",
		// Cap WAL growth: auto-checkpoint every ~4MB and never let the -wal file
		// exceed 64MB on disk (it had ballooned to 228MB).
		"PRAGMA wal_autocheckpoint=1000;",
		"PRAGMA journal_size_limit=67108864;",
	} {
		if _, err := db.Exec(p); err != nil {
			return err
		}
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := initRevokedSessions(); err != nil {
		return err
	}
	// recreate the table only if its schema changed (so the index persists across restarts)
	var existing string
	db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='files'`).Scan(&existing)
	if existing != "" && !strings.Contains(existing, "path UNINDEXED") {
		db.Exec(`DROP TABLE files;`)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS files USING fts5(
		name, path UNINDEXED, source UNINDEXED, isdir UNINDEXED, size UNINDEXED, mtime UNINDEXED,
		tokenize='trigram');`); err != nil {
		return err
	}
	var cnt int
	db.QueryRow(`SELECT count(*) FROM files`).Scan(&cnt)
	indexMu.Lock()
	indexedN = cnt
	indexMu.Unlock()
	return nil
}

// indexEmpty reports whether the index has no rows (needs an initial build).
func indexEmpty() bool { indexMu.Lock(); defer indexMu.Unlock(); return indexedN == 0 }

// noiseDirs are app/build internals not worth indexing for a file search.
var noiseDirs = map[string]bool{
	"node_modules": true, ".git": true, "lost+found": true,
	"__pycache__": true, "containerd": true, ".cache": true,
}

func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") { // hidden dirs (.fbq-cache, .filehub-tus, ...)
		return true
	}
	return noiseDirs[name]
}

// reindex rebuilds the whole index in one transaction (background goroutine).
func reindex() {
	indexMu.Lock()
	if indexing {
		indexMu.Unlock()
		return
	}
	indexing = true
	indexMu.Unlock()
	defer func() { indexMu.Lock(); indexing = false; indexMu.Unlock() }()

	start := time.Now()
	n, err := rebuildFileIndex()
	if err != nil {
		log.Printf("filehub: reindex failed: %v", err)
		return
	}
	// Fold the WAL back into the main db and truncate it — a full reindex writes a
	// lot, and without this the -wal file grows without bound (it had ballooned to 228MB).
	db.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`)
	log.Printf("filehub: indexed %d entries in %s", n, time.Since(start).Round(time.Millisecond))
	invalidateMediaCaches()
	go prewarmAll()   // freshly-indexed media → warm their thumbnails too
	go contentSweep() // ...and extract/index their text + OCR
}

func rebuildFileIndex() (int, error) {
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	hasContent := tableExists("content_meta") && tableExists("content")
	hasPhotoMeta := tableExists("photo_meta")
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM files;`); err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`INSERT INTO files(name, path, source, isdir, size, mtime) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var photoStamps map[photoMetaID]string
	if hasPhotoMeta {
		photoStamps, err = photoMetaStamps(tx)
		if err != nil {
			return 0, err
		}
	}

	scanned := make(map[string]*os.Root, len(sources))
	n := 0
	for _, s := range sources {
		var pinned *os.Root
		if hasContent {
			pinned, _ = os.OpenRoot(s.Root)
			if pinned != nil {
				defer pinned.Close()
			}
		}
		complete := pinned != nil && reindexRootPresent(pinned)
		walkErr := filepath.WalkDir(s.Root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				complete = false
				return nil
			}
			if p == s.Root {
				complete = complete && d.IsDir()
				return nil
			}
			if d.IsDir() && skipDir(d.Name()) {
				return fs.SkipDir
			}
			rel, e := filepath.Rel(s.Root, p)
			if e != nil {
				complete = false
				return nil
			}
			rel = filepath.ToSlash(rel)
			var size, mtime int64
			isdir := 0
			if d.IsDir() {
				isdir = 1
			}
			if info, e := d.Info(); e == nil {
				size = info.Size()
				mtime = info.ModTime().Unix()
				// Revisit already indexed photo metadata even outside the thumbnail
				// prewarm window. A legacy row without a stamp is refreshed once.
				if saved, hasMeta := photoStamps[photoMetaID{s.Name, rel}]; hasMeta && info.Mode().IsRegular() {
					stamp := photoFileStamp(info)
					if stamp != saved {
						if taken, lat, lon, ok := readPhotoMeta(s.Name, rel, stamp); ok {
							if _, err := tx.Exec(upsertPhotoMeta, s.Name, rel, taken, lat, lon, stamp); err != nil {
								return err
							}
						}
					}
				}
			} else {
				complete = false
			}
			if _, e := stmt.Exec(d.Name(), rel, s.Name, isdir, size, mtime); e != nil {
				return e
			}
			n++
			return nil
		})
		if walkErr != nil {
			return 0, walkErr
		}
		if complete && reindexRootPresent(pinned) {
			scanned[s.Name] = pinned
		}
	}
	if len(scanned) > 0 {
		if err := pruneMissingContentIndex(tx, scanned); err != nil {
			return 0, fmt.Errorf("prune stale text index: %w", err)
		}
	}
	if recentIndexReady() {
		if err := refreshRecentIndex(tx); err != nil {
			return 0, err
		}
	}
	indexMu.Lock()
	err = tx.Commit()
	if err == nil {
		indexedN = n
	}
	indexMu.Unlock()
	return n, err
}

func reindexRootPresent(root *os.Root) bool {
	opened, err := root.Stat(".")
	if err != nil {
		return false
	}
	current, err := os.Stat(root.Name())
	return err == nil && os.SameFile(opened, current)
}

func pruneMissingContentIndex(tx *sql.Tx, scanned map[string]*os.Root) error {
	// EXCEPT reads the filename index once; skipped paths and failed scans
	// must still be checked before removing any derived content records.
	rows, err := tx.Query(`SELECT source,path FROM content_meta EXCEPT SELECT source,path FROM files`)
	if err != nil {
		return err
	}
	defer rows.Close()
	missing := make(map[string][]string)
	for rows.Next() {
		var source, path string
		if err := rows.Scan(&source, &path); err != nil {
			return err
		}
		root := scanned[source]
		if root == nil || !filepath.IsLocal(path) || filepath.Clean(path) != path || protectedRelativePath(path) {
			continue
		}
		if _, err := root.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			missing[source] = append(missing[source], path)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for source, paths := range missing {
		if !reindexRootPresent(scanned[source]) {
			continue
		}
		for _, path := range paths {
			if _, err := tx.Exec(`DELETE FROM content WHERE rowid=(SELECT rowid_fts FROM content_meta WHERE source=? AND path=?) AND source=? AND path=?`, source, path, source, path); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM content_meta WHERE source=? AND path=?`, source, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// File types describe name extensions in the existing index. No reindex or
// filesystem reads are needed to add filters to an existing installation.
var searchFileExtensions = map[string][]string{
	"image":    {".avif", ".ico", ".svg", ".tif", ".tiff"},
	"video":    {},
	"audio":    {".aac", ".aiff", ".flac", ".m4a", ".mp3", ".ogg", ".opus", ".wav", ".wma"},
	"document": {".epub", ".md", ".markdown", ".pdf", ".tsv", ".txt"},
	"archive":  {".7z", ".bz2", ".gz", ".rar", ".tar", ".tbz", ".tbz2", ".tgz", ".xz", ".zip", ".zst"},
}

func extensionMatch(extensions []string) string {
	if slices.ContainsFunc(extensions, func(ext string) bool { return utf8.RuneCountInString(ext) < 3 }) {
		return ""
	}
	terms := make([]string, len(extensions))
	for i, extension := range extensions {
		terms[i] = strconv.Quote(extension)
	}
	return strings.Join(terms, " OR ")
}

func searchExtensionWhere(extensions []string) (string, []any) {
	var clauses []string
	var args []any
	// Narrow candidates with FTS, then check the actual end of the name.
	if match := extensionMatch(extensions); match != "" {
		clauses = append(clauses, "files MATCH ?")
		args = append(args, match)
	}
	patterns := make([]string, len(extensions))
	for i, extension := range extensions {
		patterns[i] = "lower(name) GLOB ?"
		args = append(args, "*"+extension)
	}
	clauses = append(clauses, "isdir = 0", "("+strings.Join(patterns, " OR ")+")")
	return strings.Join(clauses, " AND "), args
}

func searchFilters(query url.Values) (string, []any, bool) {
	var filters []string
	var args []any
	if source := query.Get("source"); source != "" {
		filters = append(filters, "source = ?")
		args = append(args, source)
	}
	if kind := query.Get("type"); kind != "" {
		switch kind {
		case "folder":
			filters = append(filters, "isdir = 1")
		case "file":
			filters = append(filters, "isdir = 0")
		default:
			extensions, ok := searchFileExtensions[kind]
			if !ok {
				return "", nil, false
			}
			// Reuse extensions already recognized by the thumbnail and preview paths.
			var known map[string]bool
			switch kind {
			case "image":
				known = thumbExt
			case "video":
				known = videoExt
			case "document":
				known = officeExt
			}
			extensions = slices.Clone(extensions)
			for extension := range known {
				extensions = append(extensions, extension)
			}
			slices.Sort(extensions)
			clause, values := searchExtensionWhere(extensions)
			filters = append(filters, clause)
			args = append(args, values...)
		}
	}
	if query.Get("minSize") != "" || query.Get("maxSize") != "" {
		filters = append(filters, "isdir = 0")
	}
	for _, bound := range []struct {
		param, clause string
		nonNegative   bool
	}{
		{"minSize", "size >= ?", true},
		{"maxSize", "size <= ?", true},
		{"modifiedFrom", "mtime >= ?", false},
		{"modifiedBefore", "mtime < ?", false},
	} {
		value := query.Get(bound.param)
		if value == "" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || (bound.nonNegative && n < 0) {
			return "", nil, false
		}
		filters = append(filters, bound.clause)
		args = append(args, n)
	}
	return strings.Join(filters, " AND "), args, true
}

const fileSearchPageSize = 25

type searchResponse struct {
	Results    []SearchRes       `json:"results"`
	Total      int               `json:"total"`
	Indexing   bool              `json:"indexing"`
	Indexed    int               `json:"indexed"`
	Intent     *fileSearchIntent `json:"intent,omitempty"`
	NextOffset int               `json:"nextOffset,omitempty"`
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	access := requestFolders(r.Context())
	raw, mode := strings.TrimSpace(query.Get("q")), query.Get("mode")
	q, intent := parseSearchIntent(raw, mode)
	paged := query.Has("offset")
	var offset int
	if paged {
		var err error
		offset, err = strconv.Atoi(query.Get("offset"))
		if err != nil || offset < 0 || offset > math.MaxInt-fileSearchPageSize {
			http.Error(w, "invalid search offset", http.StatusBadRequest)
			return
		}
		if mode == "auto" && !(strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`)) {
			if text, extension, without := parseFileExtension(raw, q); extension != "" {
				if intent == nil {
					intent = &fileSearchIntent{}
				}
				q, intent.Extension, intent.WithoutExtension = text, extension, without
			}
		}
	}
	indexMu.Lock()
	out := searchResponse{Results: []SearchRes{}, Indexing: indexing, Indexed: indexedN, Intent: intent}
	indexMu.Unlock()
	if access != nil {
		out.Indexed = 0
	}
	short := utf8.RuneCountInString(q) < 3 && intent == nil
	if short && !paged { // Legacy no-query requests check index status.
		writeJSON(w, out)
		return
	}
	filters, args, ok := searchFilters(query)
	if !ok {
		http.Error(w, "invalid search filters", http.StatusBadRequest)
		return
	}
	if short && filters == "" {
		writeJSON(w, out)
		return
	}
	where := "1=1"
	var matchArgs []any
	if utf8.RuneCountInString(q) >= 3 {
		where = "files MATCH ?"
		matchArgs = []any{`"` + strings.ReplaceAll(q, `"`, `""`) + `"`}
		if intent != nil {
			// The FTS table indexes names only. A filtered query may also
			// match an ancestor name without changing the legacy index.
			where = "(rowid IN (SELECT rowid FROM files WHERE files MATCH ?) OR instr(lower(path), lower(?)) > 0)"
			matchArgs = append(matchArgs, q)
		}
	} else if q != "" {
		where = "(instr(lower(name), lower(?)) > 0 OR instr(lower(path), lower(?)) > 0)"
		matchArgs = []any{q, q}
	}
	if filters != "" {
		where += " AND " + filters
	}
	if intent != nil {
		inferred, values, _ := searchFilters(intent.filters(query.Get("zone")))
		if inferred != "" {
			where += " AND " + inferred
			args = append(args, values...)
		}
		if intent.Extension != "" {
			suffix, values := searchExtensionWhere([]string{intent.Extension})
			where += " AND " + suffix
			args = append(args, values...)
		}
	}
	scope, scopeArgs := access.sqlCondition("source", "path")
	where += scope
	args = append(args, scopeArgs...)
	columns := "name, path, source, isdir, size, mtime"
	order := "isdir DESC, length(name) ASC, rowid ASC"
	limit := " LIMIT 300"
	if paged {
		limit = " LIMIT ? OFFSET ?"
		args = append(args, fileSearchPageSize+1, offset)
		if q == "" {
			// With only filters there is no name to rank. Walk the index in
			// row order so SQLite can stop once it fills the page.
			order = "rowid ASC"
		}
	} else {
		columns += ", COUNT(*) OVER()"
	}
	if key := query.Get("sort"); key != "" {
		var column string
		switch key {
		case "name":
			column = "filehub_lower(name)"
		case "size":
			column = "CAST(size AS INTEGER)"
		case "mtime":
			column = "CAST(mtime AS INTEGER)"
		default:
			http.Error(w, "invalid search sort", http.StatusBadRequest)
			return
		}
		direction := "ASC"
		if query.Get("order") == "desc" {
			direction = "DESC"
		}
		order = "isdir DESC, " + column + " " + direction + ", rowid ASC"
	}
	rows, err := db.QueryContext(r.Context(), "SELECT "+columns+" FROM files WHERE "+where+
		" ORDER BY "+order+limit, append(matchArgs, args...)...)
	if err != nil {
		http.Error(w, "file search failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var result SearchRes
		dest := []any{&result.Name, &result.Path, &result.Source, &result.IsDir, &result.Size, &result.ModTime}
		if !paged {
			dest = append(dest, &out.Total)
		}
		if err := rows.Scan(dest...); err != nil {
			http.Error(w, "file search failed", http.StatusInternalServerError)
			return
		}
		out.Results = append(out.Results, result)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "file search failed", http.StatusInternalServerError)
		return
	}
	if paged && len(out.Results) > fileSearchPageSize {
		out.NextOffset = offset + fileSearchPageSize
		out.Results = out.Results[:fileSearchPageSize]
	} else if paged && len(out.Results) > 0 {
		// The exact count is known on a nonempty final page.
		out.Total = offset + len(out.Results)
	}
	rows.Close()
	keys := make([]ownerPath, 0, len(out.Results))
	for _, item := range out.Results {
		if !item.IsDir {
			keys = append(keys, ownerPath{item.Source, item.Path})
		}
	}
	owners := fileOwnerLabels(r.Context(), keys, false)
	for i := range out.Results {
		out.Results[i].Owner = owners[ownerPath{out.Results[i].Source, out.Results[i].Path}]
	}
	writeJSON(w, out)
}

func handleReindex(w http.ResponseWriter, r *http.Request) {
	go reindex()
	writeJSON(w, map[string]any{"ok": true})
}

// ---- incremental index updates (called from file ops, so no full reindex needed) ----

func indexAdd(source, rel, name string, isdir bool, size, mtime int64) error {
	if db == nil {
		return nil
	}
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// FTS has no unique constraint. Replace this exact identity atomically.
	deleteExact := `DELETE FROM files WHERE source=? AND path=?`
	args := []any{source, rel}
	// Path and source are unindexed. Narrow candidates by the indexed basename
	// to avoid scanning the entire library on each upload. Short names have no trigrams.
	base := filepath.Base(rel)
	if utf8.RuneCountInString(base) >= 3 {
		deleteExact += ` AND name MATCH ?`
		args = append(args, `"`+strings.ReplaceAll(base, `"`, `""`)+`"`)
	}
	deleted, err := tx.Exec(deleteExact, args...)
	if err != nil {
		return err
	}
	d := 0
	if isdir {
		d = 1
	}
	if _, err = tx.Exec(`INSERT INTO files(name,path,source,isdir,size,mtime) VALUES(?,?,?,?,?,?)`, name, rel, source, d, size, mtime); err != nil {
		return err
	}
	if recentIndexReady() {
		if isdir {
			_, err = tx.Exec(`DELETE FROM file_recents WHERE source=? AND path=?`, source, rel)
		} else {
			_, err = tx.Exec(`INSERT OR REPLACE INTO file_recents(source,path,name,size,mtime) VALUES(?,?,?,?,?)`, source, rel, name, size, mtime)
		}
		if err != nil {
			return err
		}
	}
	removed, err := deleted.RowsAffected()
	if err != nil {
		return err
	}
	if err = commitIndexDelta(tx, 1-int(removed)); err != nil {
		return err
	}
	invalidateMediaCaches()
	return nil
}

// indexDelete removes rel and its whole subtree from the index. The LIKE
// pattern must escape SQLite wildcards (_ and %) or a filename like "my_file"
// would also match siblings like "myXfile/..." and wrongly drop them. The
// exact-match branch (path=?) needs no escaping.
func indexDelete(source, rel string) error {
	if db == nil || rel == "" {
		return nil
	}
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	hasContent := tableExists("content_meta") && tableExists("content")
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if hasContent {
		if err = deleteContentIndexRows(tx, source, rel); err != nil {
			return err
		}
	}
	removed, err := deleteFilenameIndexRows(tx, source, rel)
	if err != nil {
		return err
	}
	if err = commitIndexDelta(tx, -removed); err != nil {
		return err
	}
	invalidateMediaCaches()
	return nil
}

func deleteContentIndexRows(tx *sql.Tx, source, rel string) error {
	if _, err := tx.Exec(`DELETE FROM content WHERE rowid IN(SELECT rowid_fts FROM content_meta WHERE source=? AND (path=? OR substr(path,1,?)=?) AND rowid_fts IS NOT NULL)`, source, rel, utf8.RuneCountInString(rel)+1, rel+"/"); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM content_meta WHERE source=? AND (path=? OR substr(path,1,?)=?)`, source, rel, utf8.RuneCountInString(rel)+1, rel+"/")
	return err
}

func deleteFilenameIndexRows(tx *sql.Tx, source, rel string) (int, error) {
	if recentIndexReady() {
		if _, err := tx.Exec(`DELETE FROM file_recents WHERE source=? AND (path=? OR (path>=? AND path<?))`, source, rel, rel+"/", rel+"0"); err != nil {
			return 0, err
		}
	}
	deleted, err := tx.Exec(`DELETE FROM files WHERE source=? AND (path=? OR substr(path,1,?)=?)`, source, rel, utf8.RuneCountInString(rel)+1, rel+"/")
	if err != nil {
		return 0, err
	}
	removed, err := deleted.RowsAffected()
	return int(removed), err
}

func commitIndexDelta(tx *sql.Tx, delta int) error {
	indexMu.Lock()
	defer indexMu.Unlock()
	if err := tx.Commit(); err != nil {
		return err
	}
	indexedN += delta
	return nil
}

// indexAddPath indexes a file, or a directory tree rooted at full (rel = its path within source).
func indexAddPath(source, full, rel string) error {
	if db == nil {
		return nil
	}
	root, ok := byName[source]
	if !ok {
		return nil
	}
	st, err := os.Stat(full)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		if err := indexAdd(source, rel, filepath.Base(full), false, st.Size(), st.ModTime().Unix()); err != nil {
			return err
		}
		if isThumbable(full) || isVideoThumb(full) {
			return indexMediaType(source, rel, full)
		}
		return nil
	}
	if err := indexAdd(source, rel, filepath.Base(full), true, 0, st.ModTime().Unix()); err != nil {
		return err
	}
	return filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == full {
			return nil
		}
		if d.IsDir() && skipDir(d.Name()) {
			return fs.SkipDir
		}
		r, e := filepath.Rel(root, p)
		if e != nil {
			return nil
		}
		var size, mt int64
		isd := d.IsDir()
		if info, e := d.Info(); e == nil {
			if !isd {
				size = info.Size()
			}
			mt = info.ModTime().Unix()
		}
		return indexAdd(source, filepath.ToSlash(r), d.Name(), isd, size, mt)
	})
}
