package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type photoVersion struct {
	ID        string `json:"id"`
	Created   int64  `json:"created"`
	Operation string `json:"operation"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

func hashFile(full string) (string, error) {
	f, e := os.Open(full)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}

// The immutable copy and its fsync precede both the history record and replacement.
func savePhotoVersion(source, path, operation string) (string, error) {
	stored, file, err := capturePhotoVersion(source, path, operation)
	if file != nil {
		file.Close()
	}
	return stored, err
}

func capturePhotoVersion(source, path, operation string) (string, *os.File, error) {
	full, root, ok := resolveItem(source, path)
	if !ok {
		return "", nil, os.ErrInvalid
	}
	st, err := os.Lstat(full)
	if err != nil {
		return "", nil, err
	}
	if !st.Mode().IsRegular() {
		return "", nil, os.ErrInvalid
	}
	dir, err := openInternalDir(root, ".filehub-versions", true)
	if err != nil {
		return "", nil, err
	}
	defer dir.Close()
	id := randToken()
	stored := id + filepath.Ext(full)
	sourceRoot, err := os.OpenRoot(root)
	if err != nil {
		return "", nil, err
	}
	defer sourceRoot.Close()
	relative, err := filepath.Rel(root, full)
	if err != nil {
		return "", nil, err
	}
	in, err := sourceRoot.OpenFile(relative, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", nil, err
	}
	defer in.Close()
	if info, e := in.Stat(); e != nil || !info.Mode().IsRegular() {
		return "", nil, os.ErrInvalid
	}
	out, err := dir.open(stored, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return "", nil, err
	}
	retained := false
	defer func() {
		if !retained {
			out.Close()
		}
	}()
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if err == nil {
		err = out.Sync()
	}
	if err != nil {
		return "", nil, err
	}
	if err = dir.file.Sync(); err != nil {
		return "", nil, err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	_, err = db.Exec(`INSERT INTO photo_versions(id,source,path,stored_source,stored,created,operation,size,sha256) VALUES(?,?,?,?,?,?,?,?,?)`, id, source, path, source, stored, time.Now().Unix(), operation, st.Size(), hash)
	if err != nil {
		return "", nil, err
	}
	retained = true
	return stored, out, nil
}
func replacePhoto(source, path, output, operation string) error {
	original, _, ok := resolveItem(source, path)
	if !ok {
		return os.ErrInvalid
	}
	_, saved, err := capturePhotoVersion(source, path, operation)
	if err != nil {
		return err
	}
	defer saved.Close()
	if err = syncFile(output); err != nil {
		return err
	}
	if err = os.Rename(output, original); err != nil {
		return err
	}
	if err = syncFile(filepath.Dir(original)); err != nil {
		return err
	}
	if err = indexAddPath(source, original, path); err != nil {
		// Preserve the failed edited version too, then recover from the immutable copy.
		savePhotoVersion(source, path, "failed-edit")
		staged := original + ".filehub-recover"
		if e := copyVersionTo(saved, staged); e == nil {
			os.Rename(staged, original)
		}
		return err
	}
	if _, err = db.Exec(`INSERT INTO media_revision VALUES(?,?,?) ON CONFLICT(source,path) DO UPDATE SET revision=excluded.revision`, source, path, fmt.Sprint(time.Now().UnixNano())); err != nil {
		return err
	}
	if _, err = db.Exec(`DELETE FROM photo_embed WHERE source=? AND path=?`, source, path); err != nil {
		return err
	}
	contentDelete(source, path)
	if err = queueMedia(source, path); err != nil {
		return err
	}
	invalidateMediaCaches()
	return nil
}
func handleVersions(w http.ResponseWriter, r *http.Request) {
	if _, ok := safePath(r.URL.Query().Get("source"), r.URL.Query().Get("path")); !ok {
		http.Error(w, "bad path", 400)
		return
	}
	rows, err := db.Query(`SELECT id,created,operation,size,sha256 FROM photo_versions WHERE source=? AND path=? ORDER BY created DESC,id DESC`, r.URL.Query().Get("source"), r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer rows.Close()
	out := []photoVersion{}
	for rows.Next() {
		var v photoVersion
		if rows.Scan(&v.ID, &v.Created, &v.Operation, &v.Size, &v.SHA256) == nil {
			out = append(out, v)
		}
	}
	writeJSON(w, map[string]any{"versions": out})
}
func openStoredVersion(source, stored string) (*os.File, error) {
	root, ok := byName[source]
	if !ok || !privateLeaf(stored) {
		return nil, os.ErrInvalid
	}
	d, err := openInternalDir(root, ".filehub-versions", false)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.open(stored, unix.O_RDONLY, 0)
}
func copyVersionTo(in *os.File, dest string) error {
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
func handleVersionRestore(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	var source, path, storedSource, stored, hash string
	err := db.QueryRow(`SELECT source,path,stored_source,stored,sha256 FROM photo_versions WHERE id=?`, r.URL.Query().Get("id")).Scan(&source, &path, &storedSource, &stored, &hash)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	from, err := openStoredVersion(storedSource, stored)
	if err != nil {
		http.Error(w, "invalid version", 400)
		return
	}
	defer from.Close()
	h := sha256.New()
	_, err = io.Copy(h, from)
	if err != nil || hex.EncodeToString(h.Sum(nil)) != hash {
		http.Error(w, "Копия версии повреждена", 409)
		return
	}
	full, ok := safePath(source, path)
	if !ok {
		http.Error(w, "bad path", 400)
		return
	}
	dir, err := os.MkdirTemp(filepath.Dir(full), ".filehub-edit-*")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, filepath.Base(full))
	if err = copyVersionTo(from, output); err == nil {
		err = replacePhoto(source, path, output, "restore")
	}
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
