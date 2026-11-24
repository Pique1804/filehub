// Soft-delete: handleDelete moves items into a hidden per-source .filehub-trash/
// with a JSON meta sidecar instead of permanently removing them, so a fat-finger
// delete on the only copy is recoverable. A background sweep purges entries older
// than 30 days. The indexer already skips dot-dirs, so trash never shows in
// search / the photo timeline — we only hide it from the live folder listing.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const trashName = ".filehub-trash"
const trashTTL = 30 * 24 * time.Hour

func trashDir(root string) string { return filepath.Join(root, trashName) }

type trashMeta struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OrigPath  string `json:"origPath"` // path relative to the source root (incl. name)
	DeletedAt int64  `json:"deletedAt"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
}

var trashSeq int64

func validTrashID(id string) bool {
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	return true
}

// moveToTrash relocates `full` into the source's trash and records its origin.
func moveToTrash(root, full, relPath string) error {
	_, err := moveToTrashRecord(root, full, relPath)
	return err
}

func moveToTrashRecord(root, full, relPath string) (trashMeta, error) {
	d, err := openInternalDir(root, trashName, true)
	if err != nil {
		return trashMeta{}, err
	}
	defer d.Close()
	return moveToTrashPinned(root, full, relPath, d)
}

func moveToTrashPinned(root, full, relPath string, d *internalDir) (trashMeta, error) {
	st, err := os.Lstat(full)
	if err != nil {
		return trashMeta{}, err
	}
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddInt64(&trashSeq, 1))
	meta := trashMeta{id, filepath.Base(full), relPath, time.Now().Unix(), st.IsDir(), st.Size()}
	b, _ := json.Marshal(meta)
	metaPath := id + ".meta"
	if err = d.write(metaPath, b); err != nil {
		return trashMeta{}, err
	}
	if err = d.moveFrom(root, full, id); err != nil {
		if _, committed := err.(*committedMoveError); !committed {
			d.root.Remove(metaPath)
		}
		return meta, err
	}
	return meta, nil
}

func readTrash(root string) []trashMeta {
	d, err := openInternalDir(root, trashName, false)
	if err != nil {
		return []trashMeta{}
	}
	defer d.Close()
	items, err := readTrashPinned(d)
	if err != nil {
		return []trashMeta{}
	}
	return items
}

func readTrashPinned(d *internalDir) ([]trashMeta, error) {
	des, err := d.file.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := []trashMeta{}
	for _, de := range des {
		if !strings.HasSuffix(de.Name(), ".meta") {
			continue
		}
		metaID := strings.TrimSuffix(de.Name(), ".meta")
		if !validTrashID(metaID) {
			continue
		}
		if b, err := d.read(de.Name()); err == nil {
			var m trashMeta
			if json.Unmarshal(b, &m) == nil && m.ID == metaID {
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out, nil
}

func handleTrashList(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	root, ok := byName[source]
	if !ok {
		http.Error(w, "bad source", http.StatusBadRequest)
		return
	}
	items := readTrash(root)
	if account, ok := r.Context().Value(profileKey{}).(profileIdentity); ok && account.folders != nil {
		items = slices.DeleteFunc(items, func(item trashMeta) bool {
			return account.role == "viewer" || !account.folders.canWrite(source, item.OrigPath, false)
		})
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, map[string]any{"items": items, "retentionDays": int(trashTTL / (24 * time.Hour))})
}

// Call while holding fileMutationMu so scope changes and trash mutations are ordered.
func trashMutationAccess(w http.ResponseWriter, r *http.Request) (*folderAccess, bool) {
	account, ok := r.Context().Value(profileKey{}).(profileIdentity)
	if !ok {
		return nil, true // Direct handler calls in tests.
	}
	current, err := loadProfile(account.username)
	if err == nil && current.version == account.version && current.role != "viewer" {
		return current.folders, true
	}
	http.Error(w, "write access denied", http.StatusForbidden)
	return nil, false
}

func handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	access, ok := trashMutationAccess(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	source := q.Get("source")
	root, ok := byName[source]
	if !ok {
		http.Error(w, "bad source", http.StatusBadRequest)
		return
	}
	id := q.Get("id")
	if !validTrashID(id) {
		http.Error(w, "bad trash id", http.StatusBadRequest)
		return
	}
	d, err := openInternalDir(root, trashName, false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer d.Close()
	b, err := d.read(id + ".meta")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var m trashMeta
	if json.Unmarshal(b, &m) != nil || m.ID != id {
		http.Error(w, "bad meta", http.StatusInternalServerError)
		return
	}
	if !access.canWrite(source, m.OrigPath, false) {
		http.NotFound(w, r)
		return
	}
	dest, ok := safePath(source, m.OrigPath)
	if !ok || dest == root {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	dest = uniqueDest(dest) // never clobber a file that took the old name back
	if !realInside(root, dest) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	owners, err := captureFileOwnerMove(source, trashName+"/"+id)
	if err != nil {
		http.Error(w, "could not prepare owner metadata", http.StatusServiceUnavailable)
		return
	}
	rollback := func() error { return d.moveFrom(root, dest, id) }
	if access == nil {
		err = d.moveTo(root, id, dest)
	} else {
		parent, openErr := openPublicDirectory(root, parentRel(m.OrigPath))
		if openErr != nil {
			http.Error(w, "original folder is unavailable", http.StatusConflict)
			return
		}
		defer parent.Close()
		info, statErr := d.root.Lstat(id)
		if statErr != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			http.NotFound(w, r)
			return
		}
		account, err := quotaAccount(r.Context())
		if err == nil && account.personalLimit() > 0 {
			var size int64
			size, err = quotaTreeBytes(r.Context(), d.root, id)
			if err == nil {
				err = checkQuotaGrowth(r.Context(), account, size, "")
			} else {
				err = errQuotaUnavailable
			}
		}
		if writeQuotaError(w, err) {
			return
		}
		err = d.moveToParent(parent, id, filepath.Base(dest))
		rollback = func() error { return d.moveFromParent(parent, filepath.Base(dest), id) }
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rel := strings.TrimPrefix(dest, root+string(os.PathSeparator))
	if err := remapFileReferences(source, trashName+"/"+id, source, rel, owners...); err != nil {
		http.Error(w, fmt.Sprintf("restore metadata: %v; rollback: %v", err, rollback()), 503)
		return
	}
	d.root.Remove(id + ".meta")
	if err := indexAddPath(source, dest, rel); err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": rel})
}

func handleTrashDelete(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	access, ok := trashMutationAccess(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	source := q.Get("source")
	root, ok := byName[source]
	if !ok {
		http.Error(w, "bad source", http.StatusBadRequest)
		return
	}
	id := q.Get("id")
	if !validTrashID(id) {
		http.Error(w, "bad trash id", http.StatusBadRequest)
		return
	}
	d, err := openInternalDir(root, trashName, false)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer d.Close()
	if access != nil {
		b, err := d.read(id + ".meta")
		var item trashMeta
		if err != nil || json.Unmarshal(b, &item) != nil || item.ID != id || !access.canWrite(source, item.OrigPath, false) {
			http.NotFound(w, r)
			return
		}
	}
	if err := purgeTrashItem(d, source, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	access, ok := trashMutationAccess(w, r)
	if !ok {
		return
	}
	source := r.URL.Query().Get("source")
	root, ok := byName[source]
	if !ok {
		http.Error(w, "bad source", http.StatusBadRequest)
		return
	}
	if access != nil && (access.Personal == nil || access.Personal.Source != source) {
		http.Error(w, "write access denied", http.StatusForbidden)
		return
	}
	d, err := openInternalDir(root, trashName, false)
	if os.IsNotExist(err) {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	defer d.Close()
	entries, err := readTrashPinned(d)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, entry := range entries {
		if access.canWrite(source, entry.OrigPath, false) {
			if err := purgeTrashItem(d, source, entry.ID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func purgeTrashItem(d *internalDir, source, id string) error {
	if err := d.root.RemoveAll(id); err != nil {
		return err
	}
	if tableExists("file_owners") {
		if err := deleteFileOwnerTree(db, source, trashName+"/"+id); err != nil {
			return err
		}
	}
	err := d.root.Remove(id + ".meta")
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// sweepTrash purges trash entries older than trashTTL across all sources.
func sweepTrash() {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	cutoff := time.Now().Add(-trashTTL).Unix()
	for _, s := range sources {
		d, err := openInternalDir(s.Root, trashName, false)
		if err != nil {
			continue
		}
		items, _ := readTrashPinned(d)
		for _, m := range items {
			if !validTrashID(m.ID) {
				continue
			}
			if m.DeletedAt < cutoff {
				_ = purgeTrashItem(d, s.Name, m.ID)
			}
		}
		d.Close()
	}
}
