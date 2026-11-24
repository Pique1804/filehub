package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

func initIntegrity() error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS file_hashes(source TEXT,path TEXT,size INTEGER,mtime_ns INTEGER,sha256 TEXT,PRIMARY KEY(source,path))`)
	return e
}
func cacheFileHash(s, p string, st os.FileInfo, h string) {
	if tableExists("file_hashes") {
		_, err := db.Exec(`INSERT INTO file_hashes VALUES(?,?,?,?,?) ON CONFLICT(source,path) DO UPDATE SET size=excluded.size,mtime_ns=excluded.mtime_ns,sha256=excluded.sha256`, s, p, st.Size(), st.ModTime().UnixNano(), h)
		logDBError("hash cache", err)
	}
}
func currentHash(s, p string) (string, int64, error) {
	full, ok := safePath(s, p)
	if !ok {
		return "", 0, os.ErrInvalid
	}
	st, e := os.Stat(full)
	if e != nil || !st.Mode().IsRegular() {
		return "", 0, os.ErrNotExist
	}
	var h string
	if tableExists("file_hashes") && db.QueryRow(`SELECT sha256 FROM file_hashes WHERE source=? AND path=? AND size=? AND mtime_ns=?`, s, p, st.Size(), st.ModTime().UnixNano()).Scan(&h) == nil {
		return h, st.Size(), nil
	}
	h, e = hashFile(full)
	if e != nil {
		return "", 0, e
	}
	after, e := os.Stat(full)
	if e != nil || after.Size() != st.Size() || after.ModTime() != st.ModTime() {
		return "", 0, os.ErrInvalid
	}
	cacheFileHash(s, p, st, h)
	return h, st.Size(), nil
}
func resolveAlias(s, p string) (string, string) {
	if !tableExists("path_aliases") {
		return s, p
	}
	var ds, dp, old string
	// Longest path-prefix alias supports renamed directories and repeated moves.
	e := db.QueryRow(`SELECT path,dest_source,dest_path FROM path_aliases WHERE source=? AND (path=? OR substr(?,1,length(path)+1)=path||'/') ORDER BY length(path) DESC LIMIT 1`, s, p, p).Scan(&old, &ds, &dp)
	if e == nil {
		return ds, dp + strings.TrimPrefix(p, old)
	}
	return s, p
}

type receiptManifest struct {
	Source string        `json:"source"`
	Items  []fileReceipt `json:"items"`
}

type fileReceipt struct {
	Key    string `json:"key"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func handleBackupReconcile(w http.ResponseWriter, r *http.Request) {
	var req receiptManifest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req) != nil || len(req.Items) > 100 {
		http.Error(w, "bad manifest", 400)
		return
	}
	out := []map[string]any{}
	for _, it := range req.Items {
		if e := r.Context().Err(); e != nil {
			return
		}
		access := requestFolders(r.Context())
		var valid, verified bool
		if access == nil {
			valid, verified = reconcileIdentity(req.Source, it.Path, it.Size, it.SHA256)
		} else if access.canWrite(req.Source, it.Path, false) {
			if full, ok := access.readPath(req.Source, it.Path); ok {
				var err error
				valid, err = verifyFileBytes(full, it.Size, it.SHA256)
				verified = err == nil
			}
		}
		out = append(out, map[string]any{"key": it.Key, "valid": valid, "verified": verified, "path": it.Path})
	}
	writeJSON(w, map[string]any{"items": out, "checkedAt": time.Now().Unix()})
}

// An old pathname can identify several generations. Exact bytes, not the latest
// alias alone, determine which receipt is still recoverable. A verification
// error is distinct from a completed check with no matching copy.
func reconcileIdentity(source, path string, size int64, hash string) (valid, verified bool) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	type candidate struct{ source, path string }
	candidates := []candidate{{source, path}}
	hadError := false
	seen := map[candidate]bool{}
	for _, table := range []string{"path_aliases", "path_alias_history"} {
		if !tableExists(table) {
			continue
		}
		rows, err := db.Query(`SELECT path,dest_source,dest_path FROM `+table+` WHERE source=? AND (path=? OR substr(?,1,length(path)+1)=path||'/') ORDER BY length(path) DESC`, source, path, path)
		if err != nil {
			hadError = true
			continue
		}
		for rows.Next() {
			var old, ds, dp string
			if err := rows.Scan(&old, &ds, &dp); err != nil {
				hadError = true
				continue
			}
			candidates = append(candidates, candidate{ds, dp + strings.TrimPrefix(path, old)})
		}
		if rows.Err() != nil {
			hadError = true
		}
		rows.Close()
	}
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		// Internal trash paths must never authorize deleting the phone's last copy.
		full, ok := safePath(c.source, c.path)
		if !ok {
			// Deleted/internal paths are intentionally excluded; other unsafe or
			// unreadable paths cannot be described as a completed verification.
			if !strings.HasPrefix(c.path, ".filehub-") {
				hadError = true
			}
			continue
		}
		if matches, err := verifyFileBytes(full, size, hash); matches {
			return true, true
		} else if err != nil {
			hadError = true
		}
		if !tableExists("photo_versions") {
			continue
		}
		rows, err := db.Query(`SELECT stored_source,stored FROM photo_versions WHERE source=? AND path=? AND size=? AND lower(sha256)=lower(?)`, c.source, c.path, size, hash)
		if err != nil {
			hadError = true
			continue
		}
		for rows.Next() {
			var vs, vp string
			if err := rows.Scan(&vs, &vp); err != nil {
				hadError = true
				continue
			}
			if file, err := openStoredVersion(vs, vp); err == nil {
				matches, err := verifyOpenFileBytes(file, size, hash)
				file.Close()
				if matches {
					rows.Close()
					return true, true
				} else if err != nil {
					hadError = true
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				hadError = true
			}
		}
		if rows.Err() != nil {
			hadError = true
		}
		rows.Close()
	}
	return false, !hadError
}
func verifyFileBytes(full string, size int64, hash string) (bool, error) {
	file, err := os.OpenFile(full, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	return verifyOpenFileBytes(file, size, hash)
}
func verifyOpenFileBytes(file *os.File, size int64, hash string) (bool, error) {
	return verifyFileReader(file, file, size, hash)
}

func verifyFileReader(file *os.File, reader io.Reader, size int64, hash string) (bool, error) {
	before, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !before.Mode().IsRegular() || before.Size() != size {
		return false, nil
	}
	h := sha256.New()
	if _, err = io.Copy(h, reader); err != nil {
		return false, err
	}
	got := hex.EncodeToString(h.Sum(nil))
	after, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return false, os.ErrInvalid
	}
	return strings.EqualFold(got, hash), nil
}

var duplicateMu sync.Mutex
var duplicateRunning bool
var duplicateResult = map[string]any{"groups": []any{}, "checked": 0, "state": "idle"}

func handleDuplicates(w http.ResponseWriter, r *http.Request) {
	duplicateMu.Lock()
	defer duplicateMu.Unlock()
	writeJSON(w, duplicateResult)
}
func handleDuplicatesScan(w http.ResponseWriter, r *http.Request) {
	duplicateMu.Lock()
	if duplicateRunning {
		duplicateMu.Unlock()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	duplicateRunning = true
	duplicateResult = map[string]any{"groups": []any{}, "checked": 0, "state": "running"}
	duplicateMu.Unlock()
	go func() {
		defer func() { duplicateMu.Lock(); duplicateRunning = false; duplicateMu.Unlock() }()
		groups := map[int64][]Photo{}
		for _, p := range cachedAllPhotos() {
			if p.Size > 0 {
				groups[p.Size] = append(groups[p.Size], p)
			}
		}
		matches := []map[string]any{}
		checked := 0
		errors := 0
		for _, ps := range groups {
			if len(ps) < 2 {
				continue
			}
			byHash := map[string][]Photo{}
			for _, p := range ps {
				h, _, err := currentHash(p.Source, p.Path)
				if err == nil {
					byHash[h] = append(byHash[h], p)
				} else {
					errors++
				}
				checked++
				time.Sleep(20 * time.Millisecond)
			}
			for h, ps := range byHash {
				if len(ps) > 1 {
					matches = append(matches, map[string]any{"sha256": h, "size": ps[0].Size, "photos": ps})
				}
			}
			duplicateMu.Lock()
			duplicateResult = map[string]any{"groups": matches, "checked": checked, "errors": errors, "state": "running"}
			duplicateMu.Unlock()
		}
		duplicateMu.Lock()
		duplicateResult = map[string]any{"groups": matches, "checked": checked, "errors": errors, "state": "done", "completed": time.Now().Unix()}
		duplicateMu.Unlock()
	}()
	writeJSON(w, map[string]any{"ok": true})
}
