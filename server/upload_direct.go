package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

type directUpload struct {
	path   string
	size   int64
	digest string
}

func receiveDirectUpload(w http.ResponseWriter, r *http.Request, dir string, maxBody int64, hasCollision bool) (directUpload, *quotaReservation, error) {
	fileMutationMu.Lock()
	account, err := quotaAccount(r.Context())
	var reservation *quotaReservation
	var ceiling *int64
	if err == nil {
		reservation, ceiling, err = reservePersonalWrite(r.Context(), account, r.ContentLength, 0, maxBody)
	}
	fileMutationMu.Unlock()
	if errors.Is(err, errPersonalQuota) && hasCollision {
		// An identical retry can be acknowledged without another copy on disk.
		hash := sha256.New()
		size, copyErr := io.Copy(hash, http.MaxBytesReader(w, r.Body, maxBody))
		return directUpload{size: size, digest: hex.EncodeToString(hash.Sum(nil))}, nil, copyErr
	}
	if err != nil {
		return directUpload{}, nil, err
	}
	staged, err := stageDirectUpload(w, r, dir, maxBody, ceiling, hasCollision)
	if err != nil || staged.path == "" {
		reservation.release()
		reservation = nil
	}
	return staged, reservation, err
}

func stageDirectUpload(w http.ResponseWriter, r *http.Request, dir string, maxBody int64, ceiling *int64, hasCollision bool) (directUpload, error) {
	tmp, err := os.CreateTemp(dir, ".filehub-upload-*")
	if err != nil {
		return directUpload{}, err
	}
	keep := false
	defer func() {
		tmp.Close()
		if !keep {
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return directUpload{}, err
	}
	limit := maxBody
	if ceiling != nil {
		limit = *ceiling
	}
	body := http.MaxBytesReader(w, r.Body, maxBody)
	var size int64
	if hasCollision && r.ContentLength < 0 && limit < maxBody {
		// The incoming size is unknown. Hash the whole stream while writing
		// at most the reserved bytes, which are removed for an over-quota retry.
		hash := sha256.New()
		size, err = io.CopyN(io.MultiWriter(tmp, hash), body, limit)
		if err == nil {
			var rest int64
			rest, err = io.Copy(hash, body)
			size += rest
			if err == nil && rest != 0 {
				return directUpload{size: size, digest: hex.EncodeToString(hash.Sum(nil))}, nil
			}
		} else if err == io.EOF {
			err = nil
		}
	} else {
		limited := io.Reader(body)
		if limit < maxBody {
			limited = http.MaxBytesReader(w, r.Body, limit)
		}
		size, err = io.Copy(tmp, limited)
		var tooLarge *http.MaxBytesError
		if limit < maxBody && errors.As(err, &tooLarge) {
			return directUpload{}, errPersonalQuota
		}
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return directUpload{}, err
	}
	keep = true
	return directUpload{path: tmp.Name(), size: size}, nil
}

func handleDirectUploadRetry(w http.ResponseWriter, r *http.Request, directory, name string, staged directUpload) {
	select {
	case uploadPreflightReads <- struct{}{}:
		defer func() { <-uploadPreflightReads }()
	default:
		http.Error(w, "upload verification busy", http.StatusServiceUnavailable)
		return
	}
	query := r.URL.Query()
	source, path := query.Get("source"), query.Get("path")
	if !profileWriteAllowed(w, r, source, path, true) {
		return
	}
	sourcePath := byName[source]
	parent, err := filepath.Rel(sourcePath, directory)
	if err != nil {
		writeQuotaError(w, errQuotaUnavailable)
		return
	}
	root, err := os.OpenRoot(sourcePath)
	if err != nil {
		writeQuotaError(w, errQuotaUnavailable)
		return
	}
	defer root.Close()
	for attempt := 0; attempt < uploadNameAttempts && r.Context().Err() == nil; attempt++ {
		candidate := uploadCandidateName(name, attempt)
		rel := filepath.Join(parent, candidate)
		info, err := root.Lstat(rel)
		if err != nil {
			break
		}
		if !info.Mode().IsRegular() || info.Size() != staged.size {
			continue
		}
		if _, ok := safePath(source, rel); !ok {
			continue
		}
		if verifiedUploadCandidate(r.Context(), root, rel, info, staged.size, staged.digest, false) {
			if profileWriteAllowed(w, r, source, path, true) {
				writeJSON(w, map[string]any{"ok": true, "name": candidate, "size": staged.size, "identical": true})
			}
			return
		}
	}
	writeQuotaError(w, errPersonalQuota)
}
