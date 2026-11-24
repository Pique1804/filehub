package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

var dbWriteMu sync.Mutex

// Operations on originals, versions and identities are serialized together.
var fileMutationMu sync.Mutex

func tableExists(name string) bool {
	var n int
	return db != nil && db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name=?`, name).Scan(&n) == nil && n > 0
}
func initMaintenance() error {
	if err := initMediaTypes(); err != nil {
		return err
	}
	if err := initIntegrity(); err != nil {
		return err
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS media_jobs(source TEXT,path TEXT,attempts INTEGER DEFAULT 0,next_try INTEGER DEFAULT 0,error TEXT DEFAULT '',PRIMARY KEY(source,path))`,
		`CREATE TABLE IF NOT EXISTS media_revision(source TEXT,path TEXT,revision TEXT,PRIMARY KEY(source,path))`,
		`CREATE TABLE IF NOT EXISTS path_alias_history(source TEXT,path TEXT,dest_source TEXT,dest_path TEXT,PRIMARY KEY(source,path,dest_source,dest_path))`,
		`CREATE TABLE IF NOT EXISTS path_aliases(source TEXT,path TEXT,dest_source TEXT,dest_path TEXT,PRIMARY KEY(source,path))`,
		`CREATE TABLE IF NOT EXISTS photo_versions(id TEXT PRIMARY KEY,source TEXT,path TEXT,stored_source TEXT,stored TEXT,created INTEGER,operation TEXT,size INTEGER,sha256 TEXT)`,
		`CREATE TABLE IF NOT EXISTS file_owners(source TEXT NOT NULL,path TEXT NOT NULL,owner_id TEXT NOT NULL,object_id TEXT NOT NULL,PRIMARY KEY(source,path)) WITHOUT ROWID`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO path_alias_history SELECT source,path,dest_source,dest_path FROM path_aliases`)
	return err
}
func invalidateMediaCaches() {
	photoCacheMu.Lock()
	photoCacheList = nil
	photoCacheAt = 0
	photoCacheMu.Unlock()
	recentsMu.Lock()
	recentsCache = nil
	recentsAt = 0
	recentsMu.Unlock()
	embMu.Lock()
	embDirty = true
	visualRankingGeneration.Add(1)
	embMu.Unlock()
}

// Remap the entire subtree in one transaction; no old-path metadata is discarded.
func remapFileReferences(source, old, destSource, dest string, owners ...fileOwnerMove) error {
	return updateFileReferences(source, old, destSource, dest, false, owners...)
}

func updateFileReferences(source, old, destSource, dest string, trashed bool, owners ...fileOwnerMove) error {
	if db == nil {
		return nil
	}
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	tables := []string{}
	for _, t := range []string{"files", "photo_meta", "favorites", "album_photos", "photo_embed", "content", "content_meta", "shares", "media_revision", "media_jobs", "photo_versions", "file_hashes", "media_types"} {
		if tableExists(t) {
			tables = append(tables, t)
		}
	}
	removeContent := trashed && tableExists("content_meta") && tableExists("content")
	haveFileOwners := tableExists("file_owners")
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if haveFileOwners {
		if err := relocateFileOwners(tx, source, old, destSource, dest, owners); err != nil {
			return err
		}
	}
	for _, t := range tables {
		if trashed && t == "files" {
			continue // Remove the original filename index rows directly.
		}
		suffix := ""
		if t == "files" || t == "content" {
			suffix = `,name=CASE WHEN path=? THEN ? ELSE name END`
		}
		args := []any{destSource, dest, utf8.RuneCountInString(old) + 1}
		if suffix != "" {
			args = append(args, old, filepath.Base(dest))
		}
		args = append(args, source, old, utf8.RuneCountInString(old)+1, old+"/")
		if _, err = tx.Exec(`UPDATE `+t+` SET source=?,path=?||substr(path,?)`+suffix+` WHERE source=? AND (path=? OR substr(path,1,?)=?)`, args...); err != nil {
			return err
		}
	}
	if !trashed && recentIndexReady() {
		if _, err = tx.Exec(`UPDATE OR REPLACE file_recents SET source=?,path=?||substr(path,?),
			name=CASE WHEN path=? THEN ? ELSE name END WHERE source=? AND (path=? OR (path>=? AND path<?))`,
			destSource, dest, utf8.RuneCountInString(old)+1, old, filepath.Base(dest), source, old, old+"/", old+"0"); err != nil {
			return err
		}
	}
	if tableExists("path_aliases") {
		// Preserve every generation when an old filename is reused. Collapse
		// destination moves in both tables, but never replace historical origins.
		if tableExists("path_alias_history") {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO path_alias_history SELECT source,path,dest_source,dest_path FROM path_aliases`); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE OR IGNORE path_alias_history SET dest_source=?,dest_path=?||substr(dest_path,?) WHERE dest_source=? AND (dest_path=? OR substr(dest_path,1,?)=?)`, destSource, dest, utf8.RuneCountInString(old)+1, source, old, utf8.RuneCountInString(old)+1, old+"/"); err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT OR IGNORE INTO path_alias_history VALUES(?,?,?,?)`, source, old, destSource, dest); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`UPDATE path_aliases SET dest_source=?,dest_path=?||substr(dest_path,?) WHERE dest_source=? AND (dest_path=? OR substr(dest_path,1,?)=?)`, destSource, dest, utf8.RuneCountInString(old)+1, source, old, utf8.RuneCountInString(old)+1, old+"/"); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO path_aliases(source,path,dest_source,dest_path) VALUES(?,?,?,?) ON CONFLICT(source,path) DO UPDATE SET dest_source=excluded.dest_source,dest_path=excluded.dest_path`, source, old, destSource, dest); err != nil {
			return err
		}
	}
	if trashed {
		// Keep the metadata changes and index removal atomic with respect to readers.
		var removed int
		removed, err = deleteFilenameIndexRows(tx, source, old)
		if err != nil {
			return err
		}
		if removeContent {
			if err = deleteContentIndexRows(tx, destSource, dest); err != nil {
				return err
			}
		}
		err = commitIndexDelta(tx, -removed)
	} else {
		err = tx.Commit()
	}
	if err != nil {
		return err
	}
	invalidateMediaCaches()
	return nil
}

func mediaRevisionMap() map[string]string {
	out := map[string]string{}
	if !tableExists("media_revision") {
		return out
	}
	rows, err := db.Query(`SELECT source,path,revision FROM media_revision`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s, p, v string
		if rows.Scan(&s, &p, &v) == nil {
			out[s+"|"+p] = v
		}
	}
	return out
}
func queueMedia(source, path string) error {
	if db == nil || !tableExists("media_jobs") {
		return nil
	}
	_, err := db.Exec(`INSERT INTO media_jobs(source,path) VALUES(?,?) ON CONFLICT(source,path) DO UPDATE SET attempts=0,next_try=0,error=''`, source, path)
	return err
}
func mediaWorker() {
	for {
		rows, err := db.Query(`SELECT source,path FROM media_jobs WHERE attempts<5 AND next_try<=? LIMIT 20`, time.Now().Unix())
		type job struct{ s, p string }
		jobs := []job{}
		if err == nil {
			for rows.Next() {
				var j job
				if rows.Scan(&j.s, &j.p) == nil {
					jobs = append(jobs, j)
				}
			}
			rows.Close()
		}
		for _, j := range jobs {
			full, ok := safePath(j.s, j.p)
			if !ok {
				continue
			}
			st, e := os.Lstat(full)
			if e == nil && !st.Mode().IsRegular() {
				e = os.ErrInvalid
			}
			if e == nil && st.Mode().IsRegular() {
				waitRunnable()
				if isThumbable(j.p) {
					indexPhotoMeta(j.s, j.p, full)
				}
				indexOneFile(j.s, j.p)
				var n int
				if isThumbable(j.p) {
					e = db.QueryRow(`SELECT count(*) FROM photo_meta WHERE source=? AND path=?`, j.s, j.p).Scan(&n)
					if e == nil && n == 0 {
						e = errors.New("metadata not persisted")
					}
				}
				if e == nil && needsContent(j.s, j.p) && !actualVideoFile(full) {
					e = db.QueryRow(`SELECT count(*) FROM content_meta WHERE source=? AND path=? AND size=? AND mtime=?`, j.s, j.p, st.Size(), st.ModTime().Unix()).Scan(&n)
					if e == nil && n == 0 {
						e = errors.New("content not persisted")
					}
				}
				if e == nil {
					_, e = db.Exec(`DELETE FROM media_jobs WHERE source=? AND path=?`, j.s, j.p)
				}
			}
			if e != nil {
				db.Exec(`UPDATE media_jobs SET attempts=attempts+1,next_try=?,error=? WHERE source=? AND path=?`, time.Now().Add(5*time.Minute).Unix(), e.Error(), j.s, j.p)
			}
		}
		time.Sleep(5 * time.Second)
	}
}

// Stateless manifest tokens permit small deltas without keeping a server session.
// The client sends only identities and revisions, never file contents.
func photoManifestToken(items []Photo) string {
	h := sha256.New()
	for _, p := range items {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\n", p.Source, p.Path, p.Revision, p.ModTime)
	}
	return hex.EncodeToString(h.Sum(nil))
}

const photoManifestBytes = 8 << 20
const photoSnapshotPageLimit = 1000

func handlePhotosDelta(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Known map[string]string `json:"known"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, photoManifestBytes)).Decode(&req); err != nil {
		http.Error(w, "bad manifest", http.StatusBadRequest)
		return
	}
	all := profilePhotos(r)
	paged := r.URL.Query().Get("paged") == "1"
	if paged && len(all) > photoSnapshotPageLimit && len(req.Known) == 0 {
		writeJSON(w, map[string]bool{"snapshotRequired": true})
		return
	}
	changed := []Photo{}
	removed := []string{}
	current := map[string]bool{}
	for _, p := range all {
		k := p.Source + "|" + p.Path
		current[k] = true
		if req.Known[k] != p.Revision+":"+fmt.Sprint(p.ModTime) {
			changed = append(changed, p)
			if paged && len(changed) > photoSnapshotPageLimit {
				writeJSON(w, map[string]bool{"snapshotRequired": true})
				return
			}
		}
	}
	for k := range req.Known {
		if !current[k] {
			removed = append(removed, k)
			if paged && len(changed)+len(removed) > photoSnapshotPageLimit {
				writeJSON(w, map[string]bool{"snapshotRequired": true})
				return
			}
		}
	}
	writeJSON(w, map[string]any{"photos": changed, "removed": removed, "total": len(all), "token": photoManifestToken(all)})
}
func legacyBackupHealth() map[string]any {
	b, err := os.ReadFile(env("FILEHUB_ORIGINAL_BACKUP_STATUS", "/mnt/data/.filehub-originals-backup/status.json"))
	var status map[string]any
	if err != nil || json.Unmarshal(b, &status) != nil {
		status = map[string]any{"state": "unconfigured", "message": "Отдельная копия оригиналов ещё не проверена"}
	}
	completed, _ := status["lastSuccessfulCompleted"].(float64)
	if completed == 0 {
		completed, _ = status["completed"].(float64)
	}
	status["stale"] = true
	if completed > 0 {
		age := time.Since(time.Unix(int64(completed), 0))
		status["ageHours"] = int(age.Hours())
		status["stale"] = age > 36*time.Hour
	}
	if stale, _ := status["stale"].(bool); stale {
		status["freshnessWarning"] = "Нет подтверждённой копии за последние 36 часов"
	}
	var pending int
	db.QueryRow(`SELECT count(*) FROM media_jobs`).Scan(&pending)
	status["processingPending"] = pending
	return status
}

func logDBError(action string, err error) {
	if err != nil {
		log.Printf("filehub: %s: %v", action, err)
	}
}

func photoRevision(m map[string]string, s, p string, size, mtime int64) string {
	if v := m[s+"|"+p]; v != "" {
		return v
	}
	return fmt.Sprintf("%d-%d", size, mtime)
}

func handlePhotosToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"token": photoManifestToken(profilePhotos(r)), "paged": true})
}
