// Albums: user-created collections of photos (a photo can be in many albums;
// membership is stored, files are not moved). Backed by the same SQLite db.
package main

import (
	"database/sql"
	"errors"
	"html"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"
)

func initAlbums() error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS albums(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		created INTEGER NOT NULL);`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS album_photos(
		album_id INTEGER NOT NULL,
		source TEXT NOT NULL, path TEXT NOT NULL,
		added INTEGER NOT NULL,
		PRIMARY KEY(album_id, source, path));`); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := initShareOwners(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS album_shares(
		token TEXT PRIMARY KEY, album_id INTEGER NOT NULL, created INTEGER NOT NULL,
		owner_id TEXT NOT NULL DEFAULT '');`); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('album_shares') WHERE name='owner_id'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(`ALTER TABLE album_shares ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	// Legacy tokens have no verified issuer. Leave them inert and issue fresh links on request.
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS filehub_album_share_owner
		ON album_shares(album_id,owner_id) WHERE owner_id!='';
		CREATE TRIGGER IF NOT EXISTS filehub_revoke_restricted_album_shares AFTER UPDATE OF role,folder_access ON users
			WHEN NEW.folder_access IS NOT NULL OR NEW.role NOT IN ('admin','user') BEGIN
				DELETE FROM album_shares WHERE owner_id=OLD.account_id AND owner_id!='';
			END;
		CREATE TRIGGER IF NOT EXISTS filehub_revoke_deleted_album_shares AFTER DELETE ON users BEGIN
			DELETE FROM album_shares WHERE owner_id=OLD.account_id AND owner_id!='';
		END;`); err != nil {
		return err
	}
	return tx.Commit()
}

// handleAlbumShare creates (or reuses) a public link for an album.
func handleAlbumShare(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid album", http.StatusBadRequest)
		return
	}
	account, _ := requestProfile(r)
	ctx := r.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "could not share album", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO album_shares(token,album_id,created,owner_id)
		SELECT ?,a.id,?,u.account_id FROM albums a CROSS JOIN users u
		WHERE a.id=? AND u.username=? AND u.session_version=?
			AND u.account_id!='' AND u.folder_access IS NULL AND u.role IN ('admin','user')
		ON CONFLICT(album_id,owner_id) WHERE owner_id!='' DO NOTHING`,
		randToken(), time.Now().Unix(), id, account.username, account.version)
	if err != nil {
		logDBError("share album", err)
		http.Error(w, "could not share album", http.StatusServiceUnavailable)
		return
	}
	var tok string
	err = tx.QueryRowContext(ctx, `SELECT s.token FROM album_shares s
		JOIN albums a ON a.id=s.album_id JOIN users u ON u.account_id=s.owner_id
		WHERE a.id=? AND u.username=? AND u.session_version=?
			AND s.owner_id!='' AND u.folder_access IS NULL AND u.role IN ('admin','user')`,
		id, account.username, account.version).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logDBError("read album link", err)
		http.Error(w, "could not share album", http.StatusServiceUnavailable)
		return
	}
	if err = tx.Commit(); err != nil {
		logDBError("save album link", err)
		http.Error(w, "could not share album", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"url": "/a/" + tok})
}

func handleAlbumRevokeLink(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid album", http.StatusBadRequest)
		return
	}
	account, _ := requestProfile(r)
	_, err = db.ExecContext(r.Context(), `DELETE FROM album_shares WHERE album_id=? AND EXISTS(
		SELECT 1 FROM users u WHERE u.username=? AND u.session_version=? AND u.folder_access IS NULL
		AND (u.role='admin' OR (u.role='user' AND u.account_id=album_shares.owner_id AND album_shares.owner_id!='')))`,
		id, account.username, account.version)
	if err != nil {
		logDBError("revoke album link", err)
		http.Error(w, "could not revoke album link", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// albumForToken resolves a share token to its album id (0 if unknown).
func albumForToken(tok string) int64 {
	var aid int64
	db.QueryRow(`SELECT a.id FROM album_shares s JOIN albums a ON a.id=s.album_id
		JOIN users u ON u.account_id=s.owner_id
		WHERE s.token=? AND s.owner_id!='' AND u.folder_access IS NULL AND u.role IN ('admin','user')`, tok).Scan(&aid)
	return aid
}

// inSharedAlbum reports whether (source,path) belongs to the album behind this token.
func inSharedAlbum(tok, source, path string) bool {
	aid := albumForToken(tok)
	if aid == 0 {
		return false
	}
	var one int
	db.QueryRow(`SELECT 1 FROM album_photos WHERE album_id=? AND source=? AND path=?`, aid, source, path).Scan(&one)
	return one == 1
}

// handleAlbumPublic renders a public gallery page for a shared album (no auth).
func handleAlbumPublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	tok := r.PathValue("token")
	aid := albumForToken(tok)
	if aid == 0 {
		http.NotFound(w, r)
		return
	}
	var name string
	db.QueryRow(`SELECT name FROM albums WHERE id=?`, aid).Scan(&name)
	rows, _ := db.Query(`SELECT ap.source, ap.path, f.name FROM album_photos ap
		JOIN files f ON f.source=ap.source AND f.path=ap.path WHERE ap.album_id=? ORDER BY ap.added DESC`, aid)
	var cells string
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var src, path, nm string
			if rows.Scan(&src, &path, &nm) != nil {
				continue
			}
			t := "/a/" + tok + "/t?s=" + url.QueryEscape(src) + "&p=" + url.QueryEscape(path)
			f := "/a/" + tok + "/f?s=" + url.QueryEscape(src) + "&p=" + url.QueryEscape(path)
			vid := ""
			if isVideoThumb(nm) {
				vid = `<span class="pl">&#9658;</span>`
			}
			cells += `<a class="c" href="` + f + `" target="_blank" rel="noopener"><img loading="lazy" src="` + t + `">` + vid + `</a>`
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html lang="ru"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(name) + `</title>
<style>*{box-sizing:border-box}body{margin:0;background:#0f1115;color:#e8eaf0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
h1{font-size:20px;font-weight:600;padding:18px 16px 10px}
.g{display:grid;grid-template-columns:repeat(auto-fill,minmax(120px,1fr));gap:4px;padding:0 4px 24px}
.c{position:relative;aspect-ratio:1;overflow:hidden;border-radius:8px;background:#1b1e26;display:block}
.c img{width:100%;height:100%;object-fit:cover}
.pl{position:absolute;right:6px;bottom:6px;color:#fff;text-shadow:0 1px 3px #000;font-size:18px}
.ft{color:#8b90a0;font-size:12px;text-align:center;padding:8px}</style></head>
<body><h1>` + html.EscapeString(name) + `</h1><div class="g">` + cells + `</div>
<div class="ft">filehub</div></body></html>`))
}

// handleAlbumPublicThumb / Full serve a photo IF it's in the shared album (no auth).
func handleAlbumPublicThumb(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	tok := r.PathValue("token")
	src, path := r.URL.Query().Get("s"), r.URL.Query().Get("p")
	if !inSharedAlbum(tok, src, path) {
		http.NotFound(w, r)
		return
	}
	full, ok := safePath(src, path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := genThumb(r.Context(), full, st, 480)
	if err != nil {
		http.Error(w, "thumb failed", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(data)
}

func handleAlbumPublicFull(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	tok := r.PathValue("token")
	src, path := r.URL.Query().Get("s"), r.URL.Query().Get("p")
	if !inSharedAlbum(tok, src, path) {
		http.NotFound(w, r)
		return
	}
	full, ok := safePath(src, path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	originalHeaders(w, full, true)
	http.ServeFile(w, r, full)
}

func handleAlbumCreate(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	res, err := db.Exec(`INSERT INTO albums(name,created) VALUES(?,?)`, name, time.Now().Unix())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, map[string]any{"id": id, "name": name})
}

func handleAlbumDelete(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM album_photos WHERE album_id=?`, `DELETE FROM album_shares WHERE album_id=?`, `DELETE FROM albums WHERE id=?`} {
		if _, err = tx.Exec(q, id); err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 503)
		return
	}

	writeJSON(w, map[string]any{"ok": true})
}

// handleAlbumList returns each album with its photo count + a cover (newest photo).
func handleAlbumList(w http.ResponseWriter, r *http.Request) {
	type album struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Count       int    `json:"count"`
		CoverSource string `json:"coverSource"`
		CoverPath   string `json:"coverPath"`
		ShareActive bool   `json:"shareActive"`
	}
	out := []album{}
	if requestFolders(r.Context()) != nil {
		writeJSON(w, map[string]any{"albums": out})
		return
	}
	account, _ := requestProfile(r)
	rows, err := db.Query(`SELECT a.id,a.name,EXISTS(
		SELECT 1 FROM album_shares s JOIN users u ON u.account_id=s.owner_id
		WHERE s.album_id=a.id AND s.owner_id!='' AND u.folder_access IS NULL AND u.role IN ('admin','user')
			AND (?='admin' OR (?='user' AND u.username=?))
		) FROM albums a ORDER BY a.created DESC`, account.role, account.role, account.username)
	if err != nil {
		writeJSON(w, map[string]any{"albums": out})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a album
		if rows.Scan(&a.ID, &a.Name, &a.ShareActive) != nil {
			continue
		}
		db.QueryRow(`SELECT count(*) FROM album_photos WHERE album_id=?`, a.ID).Scan(&a.Count)
		// cover = most recently added photo
		db.QueryRow(`SELECT source,path FROM album_photos WHERE album_id=? ORDER BY added DESC LIMIT 1`, a.ID).Scan(&a.CoverSource, &a.CoverPath)
		out = append(out, a)
	}
	writeJSON(w, map[string]any{"albums": out})
}

func handleAlbumAdd(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	source, path := q.Get("source"), q.Get("path")
	if id == 0 || source == "" || path == "" {
		http.Error(w, "id, source, path required", http.StatusBadRequest)
		return
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO album_photos(album_id,source,path,added) VALUES(?,?,?,?)`,
		id, source, path, time.Now().Unix())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleAlbumRemove(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if _, err := db.Exec(`DELETE FROM album_photos WHERE album_id=? AND source=? AND path=?`, q.Get("id"), q.Get("source"), q.Get("path")); err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleAlbumPhotos returns the photos in an album, newest capture-date first.
func handleAlbumPhotos(w http.ResponseWriter, r *http.Request) {
	out := []Photo{}
	id := r.URL.Query().Get("id")
	rows, err := db.Query(`SELECT ap.source, ap.path, f.name, f.mtime, f.size
		FROM album_photos ap JOIN files f ON f.source=ap.source AND f.path=ap.path
		WHERE ap.album_id=?`, id)
	if err != nil {
		writeJSON(w, map[string]any{"photos": out})
		return
	}
	defer rows.Close()
	taken := photoTakenMap()
	revisions := mediaRevisionMap()
	types := mediaTypeMap()
	for rows.Next() {
		var src, path, name string
		var mt, size int64
		if rows.Scan(&src, &path, &name, &mt, &size) != nil {
			continue
		}
		date := captureDate(name, mt)
		if t, ok := taken[src+"|"+path]; ok && t > 0 {
			date = t
		}
		out = append(out, Photo{name, path, src, date, size, photoVideo(types, src, path, name), photoRevision(revisions, src, path, size, mt) + mediaTypeRevision(types, src, path, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime > out[j].ModTime })
	prewarmThumbs(out)
	writeJSON(w, map[string]any{"photos": out, "count": len(out)})
}
