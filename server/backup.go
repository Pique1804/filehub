// Database backup & restore. index.db holds the irreplaceable state — users,
// shares, albums, favorites, photo EXIF/GPS and the (slow-to-rebuild) OCR text —
// with no export path before this. We use `VACUUM INTO`, SQLite's online-backup
// primitive: it writes a consistent, compacted snapshot even while the DB is live
// with WAL, so no downtime and no torn copy.
package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func dbPath() string { return env("FILEHUB_DB", "/opt/filehub/index.db") }

// sqlQuote escapes a path for inlining into a VACUUM INTO statement. The path is
// always server-controlled (temp/backup dir), never user input, but be safe anyway.
func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// vacuumInto writes a consistent snapshot of the live DB to dst (dst must not exist).
func vacuumInto(dst string) error {
	if db == nil {
		return fmt.Errorf("db not ready")
	}
	_, err := db.Exec("VACUUM INTO " + sqlQuote(dst))
	return err
}

// handleBackup streams a fresh consistent snapshot as a download (admin only).
func handleBackup(w http.ResponseWriter, r *http.Request) {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("filehub-backup-%d.db", time.Now().UnixNano()))
	os.Remove(tmp)
	defer os.Remove(tmp)
	if err := vacuumInto(tmp); err != nil {
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	f, err := os.Open(tmp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	name := "filehub-backup-" + time.Now().Format("2006-01-02") + ".db"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if st, e := f.Stat(); e == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	io.Copy(w, f)
}

// handleRestore accepts an uploaded .db (raw request body), validates it, and
// STAGES it as <db>.restore. Swapping a live SQLite file in place is unsafe (the
// server holds it open), so the actual swap happens on the next startup via
// applyPendingRestore. Responds telling the admin to restart.
func handleRestore(w http.ResponseWriter, r *http.Request) {
	const maxRestoreBytes int64 = 2 << 30 // current DB is well below this; protects rootfs
	if r.ContentLength > maxRestoreBytes {
		http.Error(w, "backup too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreBytes)
	stage := dbPath() + ".restore"
	out, err := os.CreateTemp(filepath.Dir(stage), ".restore-*.db")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err := io.Copy(out, r.Body); err != nil {
		out.Close()
		http.Error(w, "upload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	syncErr := out.Sync()
	closeErr := out.Close()
	if syncErr != nil || closeErr != nil {
		http.Error(w, "could not save database", http.StatusInternalServerError)
		return
	}
	if err := validateSQLite(tmp); err != nil {
		http.Error(w, "not a valid filehub database: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Rename(tmp, stage); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("filehub: restore staged at %s — will apply on next restart", stage)
	writeJSON(w, map[string]any{"ok": true, "staged": true, "message": "База принята. Перезапусти сервер, чтобы применить восстановление."})
}

// validateSQLite opens path read-only and runs a quick integrity check so we never
// stage a corrupt/foreign file over the live DB.
func validateSQLite(path string) error {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&immutable=1"}
	d, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer d.Close()
	var res string
	if err := d.QueryRow("PRAGMA integrity_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("integrity_check: %s", res)
	}
	for _, query := range []string{
		`SELECT name,path,source,isdir,size,mtime FROM files LIMIT 0`,
		`SELECT id,name,created FROM albums LIMIT 0`,
		`SELECT username,pwhash,role,created FROM users LIMIT 0`,
	} {
		rows, err := d.Query(query)
		if err != nil {
			return fmt.Errorf("incompatible FileHub table schema: %w", err)
		}
		rows.Close()
	}
	var admin string
	if err := d.QueryRow(`SELECT username FROM users WHERE role='admin'
		AND length(pwhash)=60 AND substr(pwhash,1,4) IN ('$2a$','$2b$','$2y$') LIMIT 1`).Scan(&admin); err != nil || admin == "" {
		return fmt.Errorf("missing FileHub administrator")
	}
	return nil
}

// applyPendingRestore runs BEFORE initSearch opens the DB. If a validated restore
// file is staged, it replaces index.db (and drops the stale -wal/-shm) so the
// server comes up on the restored data.
func applyPendingRestore(path string) error {
	stage := path + ".restore"
	if _, err := os.Stat(stage); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := validateSQLite(stage); err != nil {
		return fmt.Errorf("invalid staged database: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		if err := snapshotBeforeRestore(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(stage, path); err != nil {
		return err
	}
	log.Printf("filehub: restore applied from staged backup")
	return nil
}

func snapshotBeforeRestore(path string) error {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=rw"}
	current, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer current.Close()
	current.SetMaxOpenConns(1)
	snapshot, err := os.CreateTemp(filepath.Dir(path), ".pre-restore-*.db")
	if err != nil {
		return err
	}
	tmp := snapshot.Name()
	defer os.Remove(tmp)
	if err := snapshot.Close(); err != nil {
		return err
	}
	if _, err := current.Exec("VACUUM INTO " + sqlQuote(tmp)); err != nil {
		return err
	}
	var busy, frames, checkpointed int
	if err = current.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); err != nil {
		return err
	}
	if busy != 0 || frames != checkpointed {
		return fmt.Errorf("database could not checkpoint before restore")
	}
	if err = current.Close(); err != nil {
		return err
	}
	if err = validateSQLite(tmp); err != nil {
		return fmt.Errorf("invalid pre-restore snapshot: %w", err)
	}
	return os.Rename(tmp, path+".pre-restore")
}

// backupSweep writes a daily snapshot to the backup dir (big disk by default) and
// keeps the newest keepN. Mirrors the trash-sweep goroutine.
func backupSweep(enabled func() bool) {
	dir := env("FILEHUB_BACKUP_DIR", filepath.Join(filepath.Dir(env("FILEHUB_DB", "/var/lib/filehub/index.db")), "db-backups"))
	const keepN = 7
	time.Sleep(20 * time.Minute) // let boot-time reindex/prewarm settle first
	for enabled() {
		runDailyBackup(dir, keepN)
		time.Sleep(24 * time.Hour)
	}
}

func runDailyBackup(dir string, keepN int) {
	if db == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("filehub: backup dir: %v", err)
		return
	}
	dst := filepath.Join(dir, "filehub-"+time.Now().Format("2006-01-02")+".db")
	if _, err := os.Stat(dst); err == nil {
		return // already have today's
	}
	tmp := dst + ".tmp"
	os.Remove(tmp)
	if err := vacuumInto(tmp); err != nil {
		log.Printf("filehub: daily backup failed: %v", err)
		os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		log.Printf("filehub: backup rename: %v", err)
		return
	}
	log.Printf("filehub: daily backup written to %s", dst)
	rotateBackups(dir, keepN)
}

func rotateBackups(dir string, keepN int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "filehub-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // date-named → lexical == chronological
	for i := 0; i < len(names)-keepN; i++ {
		os.Remove(filepath.Join(dir, names[i]))
	}
}
