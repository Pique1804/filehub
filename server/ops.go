// File operations: mkdir, delete, rename, move. All path-safe; the disk root
// itself can never be renamed/deleted/moved.
package main

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func badName(n string) bool {
	return n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\") || strings.HasPrefix(n, ".filehub-")
}

func joinRel(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func parentRel(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

func resolveItem(source, rel string) (full, root string, ok bool) {
	root, exists := byName[source]
	if !exists {
		return "", "", false
	}
	full, ok = safePath(source, rel)
	return full, root, ok
}

func handleMkdir(w http.ResponseWriter, r *http.Request) {
	if requestFolders(r.Context()) != nil {
		fileMutationMu.Lock()
		defer fileMutationMu.Unlock()
	}
	q := r.URL.Query()
	if !profileWriteAllowed(w, r, q.Get("source"), q.Get("path"), true) {
		return
	}
	name := q.Get("name")
	if badName(name) {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	dir, _, ok := resolveItem(q.Get("source"), q.Get("path"))
	if !ok {
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := indexAddPath(q.Get("source"), filepath.Join(dir, name), joinRel(q.Get("path"), name)); err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleDelete(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	q := r.URL.Query()
	if !profileWriteAllowed(w, r, q.Get("source"), q.Get("path"), false) {
		return
	}
	full, root, ok := resolveItem(q.Get("source"), q.Get("path"))
	if !ok || full == root { // never delete the disk root
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if err := deleteFileToTrash(q.Get("source"), q.Get("path")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleRename(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	q := r.URL.Query()
	if !profileWriteAllowed(w, r, q.Get("source"), q.Get("path"), false) {
		return
	}
	full, root, ok := resolveItem(q.Get("source"), q.Get("path"))
	if !ok || full == root {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	name := filepath.Base(q.Get("name"))
	if badName(name) {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	dest := filepath.Join(filepath.Dir(full), name)
	if dest != root && !strings.HasPrefix(dest, root+string(os.PathSeparator)) {
		http.Error(w, "bad dest", http.StatusBadRequest)
		return
	}
	if err := moveFileReferences(q.Get("source"), q.Get("path"), q.Get("source"), joinRel(parentRel(q.Get("path")), name)); err != nil {
		if errors.Is(err, unix.EEXIST) {
			http.Error(w, "destination exists", http.StatusConflict)
		} else {
			http.Error(w, err.Error(), 503)
		}
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleMove(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	q := r.URL.Query()
	if !profileWriteAllowed(w, r, q.Get("source"), q.Get("path"), false) {
		return
	}
	full, root, ok := resolveItem(q.Get("source"), q.Get("path"))
	if !ok || full == root {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	destSource := q.Get("destSource")
	if destSource == "" {
		destSource = q.Get("source")
	}
	if !profileWriteAllowed(w, r, destSource, q.Get("dest"), true) {
		return
	}
	destDir, destRoot, ok := resolveItem(destSource, q.Get("dest"))
	if !ok {
		http.Error(w, "bad dest", http.StatusBadRequest)
		return
	}
	if st, err := os.Stat(destDir); err != nil || !st.IsDir() {
		http.Error(w, "dest is not a directory", http.StatusBadRequest)
		return
	}
	dest := filepath.Join(destDir, filepath.Base(full))
	if dest != destRoot && !strings.HasPrefix(dest, destRoot+string(os.PathSeparator)) {
		http.Error(w, "bad dest", http.StatusBadRequest)
		return
	}
	if dest == full || strings.HasPrefix(dest, full+string(os.PathSeparator)) {
		http.Error(w, "cannot move into itself", http.StatusBadRequest)
		return
	}
	if err := moveFileReferences(q.Get("source"), q.Get("path"), destSource, joinRel(q.Get("dest"), filepath.Base(full))); err != nil {
		if errors.Is(err, unix.EEXIST) {
			http.Error(w, "destination exists", http.StatusConflict)
		} else {
			http.Error(w, err.Error(), 503)
		}
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleCopy(w http.ResponseWriter, r *http.Request) {
	if requestFolders(r.Context()) != nil {
		fileMutationMu.Lock()
		defer fileMutationMu.Unlock()
	}
	q := r.URL.Query()
	full, root, ok := resolveItem(q.Get("source"), q.Get("path"))
	if access := requestFolders(r.Context()); access != nil {
		full, ok = access.readPath(q.Get("source"), q.Get("path"))
	}
	if !ok || full == root {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	destSource := q.Get("destSource")
	if destSource == "" {
		destSource = q.Get("source")
	}
	if !profileWriteAllowed(w, r, destSource, q.Get("dest"), true) {
		return
	}
	destDir, destRoot, ok := resolveItem(destSource, q.Get("dest"))
	if !ok {
		http.Error(w, "bad dest", http.StatusBadRequest)
		return
	}
	if st, err := os.Stat(destDir); err != nil || !st.IsDir() {
		http.Error(w, "dest is not a directory", http.StatusBadRequest)
		return
	}
	dest := filepath.Join(destDir, filepath.Base(full))
	if dest != destRoot && !strings.HasPrefix(dest, destRoot+string(os.PathSeparator)) {
		http.Error(w, "bad dest", http.StatusBadRequest)
		return
	}
	if dest == full || strings.HasPrefix(dest, full+string(os.PathSeparator)) {
		http.Error(w, "cannot copy into itself", http.StatusBadRequest)
		return
	}
	if _, err := os.Lstat(dest); err == nil {
		http.Error(w, "в папке назначения уже есть объект с таким именем", http.StatusConflict)
		return
	}
	var checkCopy func(string) error
	if requestFolders(r.Context()) != nil {
		account, err := quotaAccount(r.Context())
		if writeQuotaError(w, err) {
			return
		}
		if account.personalLimit() > 0 {
			checkCopy = func(staged string) error {
				size, err := quotaPathBytes(r.Context(), staged)
				if err != nil {
					return errQuotaUnavailable
				}
				return checkQuotaGrowth(r.Context(), account, size, "")
			}
			if err := checkCopy(full); writeQuotaError(w, err) {
				return
			}
		}
	}
	if err := copyPathBeforePublish(full, dest, checkCopy); err != nil {
		if writeQuotaError(w, err) {
			return
		}
		if errors.Is(err, unix.EEXIST) {
			http.Error(w, "destination exists", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := indexAddPath(destSource, dest, joinRel(q.Get("dest"), filepath.Base(full))); err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// copyPath builds a private copy in the destination directory and publishes it
// atomically, without replacing a name another request creates mid-copy.
func copyPath(src, dst string) error {
	return copyPathBeforePublish(src, dst, nil)
}

func copyPathBeforePublish(src, dst string, check func(string) error) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fs.ErrInvalid
	}
	parent := filepath.Dir(dst)
	var tmp string
	if info.IsDir() {
		tmp, err = os.MkdirTemp(parent, ".filehub-copy-*")
	} else {
		var f *os.File
		f, err = os.CreateTemp(parent, ".filehub-copy-*")
		if err == nil {
			tmp = f.Name()
			err = f.Close()
		}
	}
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyPathStaged(src, tmp, info); err != nil {
		return err
	}
	if check != nil {
		if err := check(tmp); err != nil {
			return err
		}
	}
	if err := filepath.WalkDir(tmp, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return syncFile(p)
	}); err != nil {
		return err
	}
	if err := renameNoReplace(tmp, dst); err != nil {
		return err
	}
	return syncFile(parent)
}

// copyPathStaged only writes under a newly-created, private staging path.
func copyPathStaged(src, dst string, info os.FileInfo) error {
	if !info.IsDir() {
		return copyFileTo(src, dst, info)
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		target := filepath.Join(dst, rel)
		fi, e := d.Info()
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if p == src {
				return nil
			}
			return os.MkdirAll(target, fi.Mode().Perm())
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		return copyFileTo(p, target, fi)
	})
}

func copyFileTo(src, dst string, info os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Cross-volume moves use this copy, and upload receipts identify the original mtime.
	return os.Chtimes(dst, time.Time{}, info.ModTime())
}
