package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	tusd "github.com/tus/tusd/v2/pkg/handler"
)

// Receipts outlive the TUS staging file and the final HTTP response. They are
// private to the uploader and only describe a durable, exact file revision.
type uploadReceipt struct {
	Owner    string `json:"-"`
	Source   string `json:"source"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Modified int64  `json:"-"`
}
type storedUploadReceipt struct {
	Owner    string `json:"owner"`
	Modified int64  `json:"modified"`
	uploadReceipt
}

var uploadIDPattern = regexp.MustCompile(`^[a-f0-9]{16,64}$`)

const uploadNameAttempts = 10000

func uploadCandidateName(name string, attempt int) string {
	if attempt == 0 {
		return name
	}
	ext := filepath.Ext(name)
	return fmt.Sprintf("%s (%d)%s", name[:len(name)-len(ext)], attempt, ext)
}

func receiptPath(staging, id string) string {
	return filepath.Join(staging, ".receipts", id+".json")
}

func readUploadReceipt(staging, id string) (uploadReceipt, error) {
	var stored storedUploadReceipt
	if !uploadIDPattern.MatchString(id) {
		return stored.uploadReceipt, os.ErrNotExist
	}
	b, err := os.ReadFile(receiptPath(staging, id))
	if err != nil {
		return stored.uploadReceipt, err
	}
	if err = json.Unmarshal(b, &stored); err != nil {
		return stored.uploadReceipt, err
	}
	stored.uploadReceipt.Owner = stored.Owner
	stored.uploadReceipt.Modified = stored.Modified
	return stored.uploadReceipt, nil
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func saveUploadReceipt(staging, id string, r uploadReceipt) error {
	dir := filepath.Dir(receiptPath(staging, id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(storedUploadReceipt{Owner: r.Owner, Modified: r.Modified, uploadReceipt: r})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".receipt-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), receiptPath(staging, id)); err != nil {
		return err
	}
	return syncFile(dir)
}

func receiptCurrent(r uploadReceipt) bool {
	source, path := resolveAlias(r.Source, r.Path)
	full, ok := safePath(source, path)
	if !ok {
		return false
	}
	st, err := os.Stat(full)
	return err == nil && st.Mode().IsRegular() && st.Size() == r.Size && st.ModTime().UnixNano() == r.Modified
}

func uploadReceiptHandler(staging string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Filehub-Backup", "1")
		if req.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		u, _, ok := currentUser(req)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r, err := readUploadReceipt(staging, req.URL.Query().Get("id"))
		if err != nil || r.Owner != u {
			http.Error(w, "receipt not found", http.StatusNotFound)
			return
		}
		if access := requestFolders(req.Context()); access != nil {
			source, path := resolveAlias(r.Source, r.Path)
			if !access.canWrite(source, path, false) || !access.visibleEntry(source, path) {
				http.NotFound(w, req)
				return
			}
		}
		if !receiptCurrent(r) {
			http.Error(w, "saved file changed or missing", http.StatusGone)
			return
		}
		writeJSON(w, r)
	}
}

func receiptResponse(r uploadReceipt) tusd.HTTPResponse {
	return tusd.HTTPResponse{Header: tusd.HTTPHeader{"X-Filehub-Path": r.Path, "X-Filehub-SHA256": r.SHA256}}
}

func finishTUSUpload(h tusd.HookEvent, staging string) (tusd.HTTPResponse, error) {
	account, ok := requestProfile(&http.Request{Header: h.HTTPRequest.Header})
	owner := account.username
	if !ok || account.role == "viewer" {
		return tusd.HTTPResponse{}, fmt.Errorf("write access required")
	}
	md := h.Upload.MetaData
	if md["fh_owner"] != "" && md["fh_owner"] != owner {
		return tusd.HTTPResponse{}, fmt.Errorf("upload owner mismatch")
	}
	if r, err := readUploadReceipt(staging, h.Upload.ID); err == nil {
		if r.Owner != owner || !receiptCurrent(r) {
			return tusd.HTTPResponse{}, fmt.Errorf("saved file changed")
		}
		return receiptResponse(r), nil
	}
	dir, ok := account.folders.uploadDirectory(md["source"], md["path"])
	name := filepath.Base(md["filename"])
	if !ok || badName(name) {
		return tusd.HTTPResponse{}, fmt.Errorf("bad upload destination")
	}
	if account.folders == nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return tusd.HTTPResponse{}, err
		}
	}
	staged := h.Upload.Storage["Path"]
	f, err := os.Open(staged)
	if err != nil {
		return tusd.HTTPResponse{}, err
	}
	digest := sha256.New()
	n, err := io.Copy(digest, f)
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return tusd.HTTPResponse{}, err
	}
	if n != h.Upload.Size {
		return tusd.HTTPResponse{}, fmt.Errorf("upload size mismatch")
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	current, err := loadProfile(owner)
	if err != nil || current.version != account.version || current.role == "viewer" {
		return tusd.HTTPResponse{}, tusdDestinationDenied
	}
	if currentDir, allowed := current.folders.uploadDirectory(md["source"], md["path"]); !allowed || currentDir != dir {
		return tusd.HTTPResponse{}, tusdDestinationDenied
	}
	var dest string
	published := false
	checkedQuota := false
	for i := 0; i < uploadNameAttempts; i++ {
		dest = filepath.Join(dir, uploadCandidateName(name, i))
		if !checkedQuota && current.personalLimit() > 0 {
			if _, statErr := os.Lstat(dest); os.IsNotExist(statErr) {
				if err := tusQuotaError(checkQuotaGrowth(h.Context, current, n, h.Upload.ID)); err != nil {
					return tusd.HTTPResponse{}, err
				}
				checkedQuota = true
			} else if statErr != nil {
				return tusd.HTTPResponse{}, statErr
			} else {
				// An already published, identical file is a retry with no growth.
				if st, e := os.Lstat(dest); e == nil && st.Mode().IsRegular() {
					if same, e := filesEqual(staged, dest); e == nil && same {
						published = true
						break
					}
				}
				continue
			}
		}
		// Keep staging until the durable receipt exists. After a crash a retry
		// can compare the published bytes and finish, without another upload.
		err = os.Link(staged, dest)
		if err != nil && !errors.Is(err, os.ErrExist) {
			err = copyPath(staged, dest)
		}
		if err == nil {
			published = true
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return tusd.HTTPResponse{}, err
		}
		if current.folders != nil {
			if st, err := os.Lstat(dest); err != nil || !st.Mode().IsRegular() {
				continue
			}
		}
		if same, e := filesEqual(staged, dest); e == nil && same {
			published = true
			break
		}
	}
	if !published {
		return tusd.HTTPResponse{}, fmt.Errorf("too many filename conflicts")
	}
	if err = syncFile(dest); err != nil {
		return tusd.HTTPResponse{}, err
	}
	if err = syncFile(dir); err != nil {
		return tusd.HTTPResponse{}, err
	}
	st, err := os.Stat(dest)
	if err != nil {
		return tusd.HTTPResponse{}, err
	}
	r := uploadReceipt{Owner: owner, Source: md["source"], Path: joinRel(md["path"], filepath.Base(dest)), Size: n, SHA256: hex.EncodeToString(digest.Sum(nil)), Modified: st.ModTime().UnixNano()}
	if err = indexAddPath(r.Source, dest, r.Path); err != nil {
		return tusd.HTTPResponse{}, err
	}
	if err = queueMedia(r.Source, r.Path); err != nil {
		return tusd.HTTPResponse{}, err
	}
	if err = saveUploadReceipt(staging, h.Upload.ID, r); err != nil {
		return tusd.HTTPResponse{}, err
	}
	cacheFileHash(r.Source, r.Path, st, r.SHA256)
	os.Remove(staged)
	os.Remove(h.Upload.Storage["InfoPath"])
	return receiptResponse(r), nil
}
