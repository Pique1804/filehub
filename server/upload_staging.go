package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tus/lockfile"
)

const partialUploadRetention = 7 * 24 * time.Hour

type partialUpload struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Source   string    `json:"source"`
	Folder   string    `json:"folder"`
	Received int64     `json:"received"`
	Updated  time.Time `json:"updated"`
}

func readPartialUpload(dir, id string) (partialUpload, bool) {
	if !uploadIDPattern.MatchString(id) {
		return partialUpload{}, false
	}
	info, err := os.Lstat(filepath.Join(dir, id+".info"))
	if err != nil {
		return partialUpload{}, false
	}
	upload, err := tusUploadInfo(dir, id)
	if err != nil {
		return partialUpload{}, false
	}
	bin, err := os.Lstat(filepath.Join(dir, id))
	if err != nil || !bin.Mode().IsRegular() {
		return partialUpload{}, false
	}
	// Retain the historical behavior for completed legacy blobs with no owner.
	// An owned upload without a receipt can have all bytes staged but still
	// await publication (for example, after a quota change).
	if !upload.SizeIsDeferred && bin.Size() >= upload.Size && upload.MetaData["fh_owner"] == "" {
		return partialUpload{}, false
	}
	if _, err := os.Lstat(receiptPath(dir, id)); !errors.Is(err, os.ErrNotExist) {
		return partialUpload{}, false
	}
	updated := bin.ModTime()
	if info.ModTime().After(updated) {
		updated = info.ModTime()
	}
	return partialUpload{
		ID: id, Name: upload.MetaData["filename"], Source: upload.MetaData["source"],
		Folder: upload.MetaData["path"], Received: bin.Size(), Updated: updated,
	}, true
}

func listPartialUploads(dir string) ([]partialUpload, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	partials := []partialUpload{}
	for _, entry := range entries {
		id, found := strings.CutSuffix(entry.Name(), ".info")
		if found {
			if item, ok := readPartialUpload(dir, id); ok {
				partials = append(partials, item)
			}
		}
	}
	slices.SortFunc(partials, func(a, b partialUpload) int {
		return b.Updated.Compare(a.Updated)
	})
	return partials, nil
}

func removePartialUpload(dir, id string, expiresBefore time.Time) error {
	if !uploadIDPattern.MatchString(id) {
		return os.ErrNotExist
	}
	path, err := filepath.Abs(filepath.Join(dir, id+".lock"))
	if err != nil {
		return err
	}
	// Use tusd's exact lock file without requesting that an active transfer stop.
	lock := lockfile.Lockfile(path)
	if err := lock.TryLock(); err != nil {
		return err
	}
	defer lock.Unlock()
	item, ok := readPartialUpload(dir, id)
	if !ok || (!expiresBefore.IsZero() && !item.Updated.Before(expiresBefore)) {
		return os.ErrNotExist
	}
	if err := os.Remove(filepath.Join(dir, id)); err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, id+".info"))
}

func sweepPartialUploads(dir string) {
	expiry := time.Now().Add(-partialUploadRetention)
	items, err := listPartialUploads(dir)
	if err != nil {
		log.Printf("filehub: list partial uploads: %v", err)
		return
	}
	for _, item := range items {
		if item.Updated.Before(expiry) {
			err := removePartialUpload(dir, item.ID, expiry)
			if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, lockfile.ErrBusy) {
				log.Printf("filehub: expire partial upload: %v", err)
			}
		}
	}
}

func registerPartialUploads(mux *http.ServeMux, dir string) {
	mux.HandleFunc("GET /api/admin/uploads", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		items, err := listPartialUploads(dir)
		if err != nil {
			http.Error(w, "cannot list incomplete uploads", http.StatusServiceUnavailable)
			return
		}
		total := int64(0)
		for _, item := range items {
			total += item.Received
		}
		writeJSON(w, map[string]any{
			"items": items[:min(250, len(items))], "count": len(items), "bytes": total,
			"retentionDays": int(partialUploadRetention.Hours() / 24),
		})
	}))
	mux.HandleFunc("POST /api/admin/uploads/cancel", requireAdminMutation(func(w http.ResponseWriter, r *http.Request) {
		err := removePartialUpload(dir, r.URL.Query().Get("id"), time.Time{})
		switch {
		case errors.Is(err, os.ErrNotExist):
			http.Error(w, "partial upload no longer exists", http.StatusNotFound)
		case errors.Is(err, lockfile.ErrBusy):
			http.Error(w, "partial upload in use", http.StatusConflict)
		case err != nil:
			http.Error(w, "could not cancel partial upload", http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
}

func startPartialUploadCleanup(dir string) {
	go func() {
		sweepPartialUploads(dir)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			sweepPartialUploads(dir)
		}
	}()
}
