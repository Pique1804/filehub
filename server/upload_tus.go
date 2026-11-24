// TUS resumable uploads via embedded tusd v2. Finished blobs are moved into
// <source>/<path>/<filename> using the metadata the browser sends.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tus/tusd/v2/pkg/filelocker"
	"github.com/tus/tusd/v2/pkg/filestore"
	tusd "github.com/tus/tusd/v2/pkg/handler"
)

// moveFile renames, falling back to copy+remove across filesystems (EXDEV).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".filehub-move-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if st, statErr := in.Stat(); statErr == nil {
		out.Chmod(st.Mode().Perm())
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func registerTUS(mux *http.ServeMux, stagingDir string) error {
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}
	store := filestore.New(stagingDir)
	locker := filelocker.New(stagingDir)
	composer := tusd.NewStoreComposer()
	store.UseIn(composer)
	composer.UseCore(quotaFileStore{FileStore: store})
	locker.UseIn(composer)

	onFinish := func(hook tusd.HookEvent) (tusd.HTTPResponse, error) {
		return finishTUSUpload(hook, stagingDir)
	}
	onCreate := func(hook tusd.HookEvent) (tusd.HTTPResponse, tusd.FileInfoChanges, error) {
		account, ok := requestProfile(&http.Request{Header: hook.HTTPRequest.Header})
		if !ok || account.role == "viewer" {
			return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, fmt.Errorf("write access required")
		}
		md := tusd.MetaData{}
		for k, v := range hook.Upload.MetaData {
			md[k] = v
		}
		if account.folders != nil {
			if _, allowed := account.folders.uploadDirectory(md["source"], md["path"]); !allowed || badName(md["filename"]) {
				return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusdDestinationDenied
			}
		}
		md["fh_owner"] = account.username
		md["fh_scope_version"] = account.version
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{MetaData: md}, nil
	}

	handler, err := tusd.NewHandler(tusd.Config{
		BasePath:                  "/files/",
		MaxSize:                   maxUploadBytes(),
		StoreComposer:             composer,
		PreFinishResponseCallback: onFinish,
		PreUploadCreateCallback:   onCreate,
	})
	if err != nil {
		return err
	}
	mux.HandleFunc("/api/backup/status", requireBrowse(uploadReceiptHandler(stagingDir)))
	mux.HandleFunc("/api/upload/preflight", requireUploadMutation(handleUploadPreflight))
	mux.HandleFunc("GET /api/profile/quota", requireBrowse(handlePersonalQuota))
	registerPartialUploads(mux, stagingDir)
	mux.Handle("/files/", authorizeTUS(stagingDir, http.StripPrefix("/files/", handler)))
	mux.Handle("/files", authorizeTUS(stagingDir, http.StripPrefix("/files", handler)))
	quotaTUSDirectory = stagingDir
	return nil
}

type quotaFileStore struct{ filestore.FileStore }

func (store quotaFileStore) NewUpload(ctx context.Context, info tusd.FileInfo) (tusd.Upload, error) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	account, err := loadProfile(info.MetaData["fh_owner"])
	if err != nil || account.version != info.MetaData["fh_scope_version"] || account.role == "viewer" {
		return nil, tusdDestinationDenied
	}
	if account.folders != nil {
		if _, ok := account.folders.uploadDirectory(info.MetaData["source"], info.MetaData["path"]); !ok {
			return nil, tusdDestinationDenied
		}
		if account.personalLimit() > 0 && info.SizeIsDeferred {
			return nil, tusQuotaNeedsLength
		}
		if err := tusQuotaError(checkQuotaGrowth(ctx, account, info.Size, "")); err != nil {
			return nil, err
		}
	}
	return store.FileStore.NewUpload(ctx, info)
}

var tusdDestinationDenied = tusd.NewError("ERR_FOLDER_ACCESS", "upload is outside the personal folder", http.StatusForbidden)

func authorizeTUS(staging string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, ok := requestProfile(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if account.role == "viewer" {
			http.Error(w, "read-only access", http.StatusForbidden)
			return
		}
		id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/files"), "/")
		usesExisting := r.Method == http.MethodHead || r.Method == http.MethodPatch || r.Method == http.MethodDelete
		var info tusd.FileInfo
		var infoErr error
		if usesExisting {
			info, infoErr = tusUploadInfo(staging, id)
			if owner := info.MetaData["fh_owner"]; infoErr == nil && owner != "" && owner != account.username {
				http.NotFound(w, r)
				return
			}
		}
		if account.folders != nil {
			if r.Header.Get("Upload-Concat") != "" {
				http.Error(w, "upload concatenation unavailable for this profile", http.StatusForbidden)
				return
			}
			switch {
			case r.Method == http.MethodOptions:
			case r.Method == http.MethodPost && id == "":
				// The creation hook validates destination and stamps the owner.
			case usesExisting:
				if infoErr != nil || !ownsTUSInfo(info, account) {
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodPatch && account.personalLimit() > 0 && info.SizeIsDeferred {
					http.Error(w, "Restart this upload with a known file size.", http.StatusLengthRequired)
					return
				}
				if account.personalLimit() > 0 && r.Method == http.MethodHead {
					if blob, err := os.Lstat(filepath.Join(staging, id)); err == nil && info.Size > 0 && blob.Size() >= info.Size {
						if _, err := readUploadReceipt(staging, id); err != nil {
							fileMutationMu.Lock()
							current, quotaErr := quotaAccount(withProfile(r, account).Context())
							if quotaErr == nil {
								quotaErr = checkQuotaGrowth(r.Context(), current, info.Size, id)
							}
							fileMutationMu.Unlock()
							if writeQuotaError(w, quotaErr) {
								return
							}
							http.Error(w, "Upload data has arrived but the file has not been published. Check the quota or cancel this incomplete upload.", http.StatusConflict)
							return
						}
					}
				}
			default:
				http.Error(w, "unsupported upload request", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, withProfile(r, account))
	})
}

func ownsTUSInfo(upload tusd.FileInfo, account profileIdentity) bool {
	if upload.MetaData["fh_owner"] != account.username || upload.MetaData["fh_scope_version"] != account.version {
		return false
	}
	_, allowed := account.folders.uploadDirectory(upload.MetaData["source"], upload.MetaData["path"])
	return allowed
}

func tusUploadInfo(staging, id string) (tusd.FileInfo, error) {
	var upload tusd.FileInfo
	if !uploadIDPattern.MatchString(id) {
		return upload, os.ErrNotExist
	}
	path := filepath.Join(staging, id+".info")
	info, err := os.Lstat(path)
	if err != nil {
		return upload, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return upload, os.ErrInvalid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return upload, err
	}
	if json.Unmarshal(data, &upload) != nil || upload.ID != id || upload.Size < 0 {
		return upload, os.ErrInvalid
	}
	return upload, nil
}
