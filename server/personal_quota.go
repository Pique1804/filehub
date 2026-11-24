package main

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	tusd "github.com/tus/tusd/v2/pkg/handler"
)

const quotaFullMessage = "Personal storage limit reached. Free space, cancel incomplete uploads, or ask the administrator to raise the limit."

var (
	errPersonalQuota    = errors.New(quotaFullMessage)
	errQuotaUnavailable = errors.New("Could not check personal storage usage.")
	tusQuotaFull        = tusd.NewError("ERR_PERSONAL_QUOTA", quotaFullMessage, http.StatusInsufficientStorage)
	tusQuotaNeedsLength = tusd.NewError("ERR_QUOTA_LENGTH", "Specify the upload length for personal storage.", http.StatusLengthRequired)
	tusQuotaUnavailable = tusd.NewError("ERR_QUOTA_UNAVAILABLE", errQuotaUnavailable.Error(), http.StatusServiceUnavailable)
	quotaTUSDirectory   string // Set by registerTUS before the HTTP server starts.
)

type personalQuotaUsage struct {
	Limit     int64 `json:"limitBytes"`
	Used      int64 `json:"usedBytes"`
	Reserved  int64 `json:"reservedBytes"`
	Available int64 `json:"availableBytes"`
}

type quotaReservation struct {
	key   folderGrant
	bytes int64
}

// Short-lived, non-resumable writes hold capacity until they publish or abort.
// All accesses use fileMutationMu, together with TUS admission and publication.
var activeQuotaReservations = make(map[folderGrant]int64)

// Reservations belong to the storage folder. Rotating credentials must not
// make staged bytes disappear from subsequent quota checks.
func quotaKey(account profileIdentity) folderGrant {
	return *account.folders.Personal
}

func (reservation *quotaReservation) releaseLocked() {
	if reservation == nil || reservation.bytes == 0 {
		return
	}
	remaining := activeQuotaReservations[reservation.key] - reservation.bytes
	if remaining == 0 {
		delete(activeQuotaReservations, reservation.key)
	} else {
		activeQuotaReservations[reservation.key] = remaining
	}
	reservation.bytes = 0
}

func (reservation *quotaReservation) release() {
	if reservation == nil {
		return
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	reservation.releaseLocked()
}

// A negative length is streamed, so reserve its maximum permitted size.
func reservePersonalWrite(ctx context.Context, account profileIdentity, length, existing, maxBody int64) (*quotaReservation, *int64, error) {
	usage, err := measurePersonalQuota(ctx, account, "")
	if err != nil || usage == nil {
		return nil, nil, err
	}
	ceiling := min(maxBody, addQuotaBytes(usage.Available, existing))
	if length > ceiling {
		return nil, nil, errPersonalQuota
	}
	if length < 0 {
		length = ceiling
	}
	bytes := max(0, length-existing)
	if bytes == 0 {
		return nil, &ceiling, nil
	}
	key := quotaKey(account)
	activeQuotaReservations[key] = addQuotaBytes(activeQuotaReservations[key], bytes)
	return &quotaReservation{key, bytes}, &ceiling, nil
}

func validFolderQuota(limit int64, personal *folderGrant) bool {
	return limit >= 0 && (limit == 0 || personal != nil)
}

func (account profileIdentity) personalLimit() int64 {
	if account.folders == nil || account.folders.Personal == nil {
		return 0
	}
	return account.folders.QuotaBytes
}

func quotaAccount(ctx context.Context) (profileIdentity, error) {
	account, present := ctx.Value(profileKey{}).(profileIdentity)
	if !present {
		return profileIdentity{}, nil
	}
	current, err := loadProfile(account.username)
	if err != nil || current.version != account.version {
		return profileIdentity{}, errQuotaUnavailable
	}
	return current, nil
}

func addQuotaBytes(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// Use visible logical bytes, including ordinary hidden files. Internal staging,
// server metadata and symlinks do not count as published data.
func quotaTreeBytes(ctx context.Context, root *os.Root, relative string) (int64, error) {
	var total int64
	err := fs.WalkDir(root.FS(), relative, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path != relative && strings.HasPrefix(entry.Name(), ".filehub-") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				total = addQuotaBytes(total, info.Size())
			}
		}
		return nil
	})
	return total, err
}

func quotaPathBytes(ctx context.Context, full string) (int64, error) {
	root, err := os.OpenRoot(filepath.Dir(full))
	if err != nil {
		return 0, err
	}
	defer root.Close()
	return quotaTreeBytes(ctx, root, filepath.Base(full))
}

// A tusd .info record is the durable reservation. Keep the declared size across
// resumptions and restarts, including a completed body awaiting publication.
func quotaTUSReservations(ctx context.Context, account profileIdentity, excludeID string) (int64, error) {
	if quotaTUSDirectory == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(quotaTUSDirectory)
	if err != nil {
		return 0, err
	}
	var reserved int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		id, ok := strings.CutSuffix(entry.Name(), ".info")
		if !ok || id == excludeID || !uploadIDPattern.MatchString(id) {
			continue
		}
		info, err := tusUploadInfo(quotaTUSDirectory, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		// A deferred upload created before quota was enabled can temporarily
		// rewrite its metadata. It cannot grow while quota is active.
		if err != nil {
			return 0, err
		}
		if info.MetaData["fh_owner"] != account.username || info.MetaData["fh_scope_version"] != account.version ||
			!account.folders.canWrite(info.MetaData["source"], info.MetaData["path"], true) {
			continue
		}
		if _, err := readUploadReceipt(quotaTUSDirectory, id); err == nil {
			continue
		}
		bin, err := os.Lstat(filepath.Join(quotaTUSDirectory, id))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !bin.Mode().IsRegular() {
			return 0, errQuotaUnavailable
		}
		reserved = addQuotaBytes(reserved, max(info.Size, bin.Size()))
	}
	return reserved, nil
}

// Called under fileMutationMu for decisions about admission and publication.
func measurePersonalQuota(ctx context.Context, account profileIdentity, excludeID string) (*personalQuotaUsage, error) {
	limit := account.personalLimit()
	if limit == 0 {
		return nil, nil
	}
	grant := account.folders.Personal
	full, ok := realProfilePath(grant.Source, grant.Path)
	if !ok {
		return nil, errQuotaUnavailable
	}
	used, err := quotaPathBytes(ctx, full)
	if err != nil {
		return nil, errQuotaUnavailable
	}
	reserved, err := quotaTUSReservations(ctx, account, excludeID)
	if err != nil {
		return nil, errQuotaUnavailable
	}
	reserved = addQuotaBytes(reserved, activeQuotaReservations[quotaKey(account)])
	return &personalQuotaUsage{Limit: limit, Used: used, Reserved: reserved, Available: max(0, limit-addQuotaBytes(used, reserved))}, nil
}

func checkQuotaGrowth(ctx context.Context, account profileIdentity, growth int64, excludeID string) error {
	if growth <= 0 {
		return nil
	}
	usage, err := measurePersonalQuota(ctx, account, excludeID)
	if err == nil && usage != nil && growth > usage.Available {
		return errPersonalQuota
	}
	return err
}

func checkRequestQuota(w http.ResponseWriter, r *http.Request, growth int64) bool {
	account, err := quotaAccount(r.Context())
	if err == nil {
		err = checkQuotaGrowth(r.Context(), account, growth, "")
	}
	return !writeQuotaError(w, err)
}

func writeQuotaError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errPersonalQuota):
		w.Header().Set("X-Filehub-Quota", "full")
		http.Error(w, quotaFullMessage, http.StatusInsufficientStorage)
	case errors.Is(err, errQuotaUnavailable):
		http.Error(w, errQuotaUnavailable.Error(), http.StatusServiceUnavailable)
	default:
		return false
	}
	return true
}

func tusQuotaError(err error) error {
	if errors.Is(err, errPersonalQuota) {
		return tusQuotaFull
	}
	if err != nil {
		return tusQuotaUnavailable
	}
	return nil
}

func handlePersonalQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	account, err := quotaAccount(r.Context())
	if writeQuotaError(w, err) {
		return
	}
	usage, err := measurePersonalQuota(r.Context(), account, "")
	if !writeQuotaError(w, err) {
		writeJSON(w, map[string]any{"quota": usage})
	}
}
