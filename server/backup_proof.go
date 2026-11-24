package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var backupSnapshotName = regexp.MustCompile(`^snapshot-[0-9]{8}T[0-9]{12}Z$`)
var backupInstanceName = regexp.MustCompile(`^[a-f0-9]{32}$`)
var backupProofSlots = make(chan struct{}, 2)

func (b *managedBackup) health(w http.ResponseWriter, _ *http.Request) {
	status := legacyBackupHealth()
	b.mu.Lock()
	state := b.current
	b.mu.Unlock()
	if state.Settings.Enabled || state.LastCopy != nil || state.Run.State != "idle" {
		managed := map[string]any{"state": state.Run.State, "fileChecks": state.LastCopy != nil}
		if state.LastCopy != nil {
			completed := state.LastCopy.Run.Completed
			managed["completed"] = completed
			managed["previousSettings"] = state.LastCopy.Run.SettingsRevision != state.Revision
			// Allow the scheduled job its full running window before marking the last result stale.
			if state.Settings.Enabled {
				slot := backupSlot(state.Settings, time.Now().Add(-2*time.Hour), false).Unix()
				managed["stale"] = slot > max(completed, state.SavedAt)
			}
		}
		status["managedCopy"] = managed
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status)
}

func (b *managedBackup) checkCopies(w http.ResponseWriter, r *http.Request) {
	var request receiptManifest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request) != nil || len(request.Items) > 20 {
		http.Error(w, "bad manifest", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	select {
	case backupProofSlots <- struct{}{}:
		defer func() { <-backupProofSlots }()
	default:
		http.Error(w, "backup verification busy", http.StatusServiceUnavailable)
		return
	}
	b.mu.Lock()
	evidence := b.current.LastCopy
	b.mu.Unlock()
	root, err := openBackupSnapshot(evidence)
	if root != nil {
		defer root.Close()
	}
	results := make([]map[string]string, 0, len(request.Items))
	for _, receipt := range request.Items {
		if r.Context().Err() != nil {
			return
		}
		state := "unknown"
		if err != nil {
			state = "unavailable"
		} else if root != nil {
			state = verifyBackupReceipt(r.Context(), root, evidence.Plan, request.Source, receipt)
		}
		results = append(results, map[string]string{"key": receipt.Key, "state": state})
	}
	writeJSON(w, map[string]any{"items": results})
}

func openBackupSnapshot(copy *backupCopy) (*os.Root, error) {
	if copy == nil {
		return nil, nil
	}
	plan := copy.Plan
	if !backupInstanceName.MatchString(plan.Instance) || !backupSnapshotName.MatchString(copy.Run.Snapshot) {
		return nil, os.ErrInvalid
	}
	destination, err := os.OpenRoot(plan.Destination.FullPath)
	if err != nil {
		return nil, err
	}
	defer destination.Close()
	info, err := destination.Stat(".")
	if err != nil {
		return nil, err
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(identity.Dev) != plan.Destination.Device || uint64(identity.Ino) != plan.Destination.Inode {
		return nil, os.ErrInvalid
	}
	name := filepath.Join(managedBackupDirectory, plan.Instance, copy.Run.Snapshot)
	for _, path := range []string{managedBackupDirectory, filepath.Dir(name), name} {
		info, err := destination.Lstat(path)
		if err != nil {
			return nil, err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
			return nil, os.ErrPermission
		}
	}
	return destination.OpenRoot(name)
}

func verifyBackupReceipt(ctx context.Context, root *os.Root, plan backupPlan, source string, item fileReceipt) string {
	rel := filepath.Clean(item.Path)
	if !filepath.IsLocal(rel) || rel == "." || protectedRelativePath(rel) || len(item.SHA256) != 64 || item.Size < 0 {
		return "unknown"
	}
	if _, err := hex.DecodeString(item.SHA256); err != nil {
		return "unknown"
	}
	included := false
	for _, folder := range plan.Folders {
		if source == folder.Source && (folder.Path == "" || strings.HasPrefix(rel, folder.Path+"/")) {
			included = true
			break
		}
	}
	if !included {
		return "unconfirmed"
	}
	live, ok := safePath(source, rel)
	if !ok {
		return "unknown"
	}
	info, err := os.Stat(live)
	if err != nil || !info.Mode().IsRegular() {
		return "unknown"
	}
	origin, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "unknown"
	}

	// Match the worker's sources/<sha256 of exact source name>/<relative path> layout.
	key := sha256.Sum256([]byte(source))
	copied := filepath.Join("sources", hex.EncodeToString(key[:]), rel)
	file, err := root.OpenFile(copied, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "unconfirmed"
	}
	if err != nil {
		return "unavailable"
	}
	defer file.Close()
	copiedInfo, err := file.Stat()
	if err != nil {
		return "unavailable"
	}
	volume, ok := copiedInfo.Sys().(*syscall.Stat_t)
	if !ok || uint64(volume.Dev) != plan.Destination.Device {
		return "unavailable"
	}
	matches, err := verifyFileReader(file, backupHashReader{ctx, file}, item.Size, item.SHA256)
	if err != nil {
		return "unavailable"
	}
	if !matches {
		return "unconfirmed"
	}
	if origin.Dev == volume.Dev {
		return "same_volume"
	}
	return "confirmed"
}

type backupHashReader struct {
	ctx  context.Context
	file *os.File
}

func (r backupHashReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.file.Read(data)
}
