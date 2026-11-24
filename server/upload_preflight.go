package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type uploadPreflight struct {
	Source   string `json:"source"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     *int64 `json:"size"`
	SHA256   string `json:"sha256"`
}

// Bound the number of full disk reads started by preflight requests.
var uploadPreflightReads = make(chan struct{}, 3)

func handleUploadPreflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req uploadPreflight
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if err := decoder.Decode(&req); err != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "bad upload preflight", http.StatusBadRequest)
		return
	}
	if req.Size == nil || *req.Size < 0 || badName(req.Filename) || strings.ContainsRune(req.Filename, 0) ||
		(req.Path != "" && (!filepath.IsLocal(req.Path) || filepath.ToSlash(filepath.Clean(req.Path)) != req.Path)) {
		http.Error(w, "bad upload destination or size", http.StatusBadRequest)
		return
	}
	if *req.Size > maxUploadBytes() {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}
	if req.SHA256 != "" {
		if digest, err := hex.DecodeString(req.SHA256); err != nil || len(digest) != 32 {
			http.Error(w, "bad sha256", http.StatusBadRequest)
			return
		}
		select {
		case uploadPreflightReads <- struct{}{}:
			defer func() { <-uploadPreflightReads }()
		default:
			http.Error(w, "upload preflight busy", http.StatusServiceUnavailable)
			return
		}
	}
	access := requestFolders(r.Context())
	if _, ok := access.uploadDirectory(req.Source, req.Path); !ok {
		if access != nil {
			http.Error(w, "upload is outside the personal folder", http.StatusForbidden)
			return
		}
		http.Error(w, "bad upload destination", http.StatusBadRequest)
		return
	}
	root, err := os.OpenRoot(byName[req.Source])
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer root.Close()
	for attempt := 0; attempt < uploadNameAttempts; attempt++ {
		if r.Context().Err() != nil {
			return
		}
		rel := joinRel(req.Path, uploadCandidateName(req.Filename, attempt))
		st, err := root.Lstat(rel)
		if errors.Is(err, os.ErrNotExist) {
			// The normal publisher would claim this available name.
			break
		}
		if err != nil {
			break
		}
		if !st.Mode().IsRegular() || st.Size() != *req.Size {
			continue
		}
		if _, ok := safePath(req.Source, rel); !ok {
			continue
		}
		if req.SHA256 == "" {
			// A name and size match lets the client avoid hashing new files twice.
			writeJSON(w, map[string]bool{"candidate": true})
			return
		}
		if verifiedUploadCandidate(r.Context(), root, rel, st, *req.Size, req.SHA256, true) {
			if _, ok := safePath(req.Source, rel); ok {
				writeJSON(w, uploadReceipt{Source: req.Source, Path: rel, Size: *req.Size, SHA256: strings.ToLower(req.SHA256)})
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type uploadPreflightReader struct {
	ctx  context.Context
	file *os.File
}

func (r uploadPreflightReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(p)
}

func verifiedUploadCandidate(ctx context.Context, root *os.Root, path string, before os.FileInfo, size int64, hash string, syncReceipt bool) bool {
	file, err := root.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || before.ModTime() != opened.ModTime() {
		return false
	}
	matched, err := verifyFileReader(file, uploadPreflightReader{ctx: ctx, file: file}, size, hash)
	if err != nil || !matched || ctx.Err() != nil {
		return false
	}
	if syncReceipt {
		if file.Sync() != nil {
			return false
		}
		parent, err := root.Open(filepath.Dir(path))
		if err != nil {
			return false
		}
		defer parent.Close()
		if parent.Sync() != nil {
			return false
		}
	}
	current, err := root.Lstat(path)
	return err == nil && os.SameFile(opened, current) && current.Size() == size && opened.ModTime() == current.ModTime()
}
