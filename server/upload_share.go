package main

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
)

const uploadShareMiB = 1 << 20

//go:embed upload_share.html
var uploadSharePage string

var receiveFileTemplate = template.Must(template.New("receive-file").Parse(uploadSharePage))
var receiveUploadSlots = make(chan struct{}, 3)
var activeGuestLinks sync.Map

type guestUploadReader struct {
	reader     io.Reader
	controller *http.ResponseController
	end        time.Time
}

func (r guestUploadReader) Read(p []byte) (int, error) {
	deadline := time.Now().Add(45 * time.Second)
	if r.end.Before(deadline) {
		deadline = r.end
	}
	r.controller.SetReadDeadline(deadline)
	return r.reader.Read(p)
}

type uploadShare struct {
	source, path, folderID, conflict string
	maxBytes                         int64
}

func initUploadShares() error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := initShareOwners(tx); err != nil {
		return err
	}
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS upload_shares (
		token TEXT PRIMARY KEY, owner TEXT NOT NULL, source TEXT NOT NULL, path TEXT NOT NULL,
		folder_id TEXT NOT NULL, expires INTEGER NOT NULL, created INTEGER NOT NULL,
		max_bytes INTEGER NOT NULL, max_files INTEGER NOT NULL, conflict TEXT NOT NULL,
		received INTEGER NOT NULL DEFAULT 0, owner_id TEXT NOT NULL DEFAULT '')`)
	if err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('upload_shares') WHERE name='owner_id'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(`ALTER TABLE upload_shares ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE upload_shares SET expires=1 WHERE owner_id='' AND expires>1;
		CREATE TRIGGER IF NOT EXISTS filehub_revoke_restricted_uploads AFTER UPDATE OF role,folder_access ON users
		WHEN NEW.folder_access IS NOT NULL OR NEW.role NOT IN ('admin','user') BEGIN
			UPDATE upload_shares SET expires=1 WHERE owner_id=OLD.account_id AND expires>1;
		END;
		CREATE TRIGGER IF NOT EXISTS filehub_revoke_deleted_uploads AFTER DELETE ON users BEGIN
			UPDATE upload_shares SET expires=1 WHERE owner_id=OLD.account_id AND expires>1;
		END;`); err != nil {
		return err
	}
	return tx.Commit()
}

func openReceiveFolder(source, path string) (*os.File, string, error) {
	full, root, ok := resolveItem(source, path)
	if !ok {
		return nil, "", os.ErrInvalid
	}
	relative, err := filepath.Rel(root, full)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, "", os.ErrInvalid
	}
	storage, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	defer storage.Close()
	folder, err := storage.OpenFile(relative, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, "", err
	}
	var info unix.Stat_t
	if err := unix.Fstat(int(folder.Fd()), &info); err != nil || !publicPathInside(root, full) {
		folder.Close()
		return nil, "", os.ErrInvalid
	}
	return folder, fmt.Sprintf("%d:%d", info.Dev, info.Ino), nil
}

func handleUploadShareCreate(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	account, _ := requestProfile(r)
	source, path := r.PostFormValue("source"), r.PostFormValue("path")
	days, _ := strconv.Atoi(r.PostFormValue("days"))
	maxMiB, _ := strconv.ParseInt(r.PostFormValue("maxMiB"), 10, 64)
	maxFiles, _ := strconv.Atoi(r.PostFormValue("maxFiles"))
	conflict := r.PostFormValue("conflict")
	if days < 1 || days > 365 || maxFiles < 1 || maxFiles > 100 || maxMiB < 1 ||
		(conflict != "rename" && conflict != "reject") ||
		(path != "" && (!filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path)) {
		http.Error(w, "invalid upload link settings", http.StatusBadRequest)
		return
	}
	if maxMiB > maxUploadBytes()/uploadShareMiB {
		http.Error(w, "upload limit exceeds server limit", http.StatusRequestEntityTooLarge)
		return
	}
	folder, id, err := openReceiveFolder(source, path)
	if err != nil {
		http.Error(w, "destination folder unavailable", http.StatusNotFound)
		return
	}
	defer folder.Close()
	now := time.Now()
	token := randToken()
	result, err := db.Exec(`INSERT INTO upload_shares(token,owner,source,path,folder_id,expires,created,max_bytes,max_files,conflict,owner_id)
		SELECT ?,username,?,?,?,?,?,?,?,?,account_id FROM users
		WHERE username=? AND session_version=? AND folder_access IS NULL AND role IN ('admin','user') AND account_id!=''`,
		token, source, path, id, now.AddDate(0, 0, days).Unix(), now.Unix(), maxMiB*uploadShareMiB, maxFiles, conflict, account.username, account.version)
	if err != nil {
		logDBError("create upload link", err)
		http.Error(w, "upload link unavailable", http.StatusServiceUnavailable)
		return
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted != 1 {
		http.Error(w, "upload link access changed", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]string{"token": token, "url": "/u/" + token})
}

func handleUploadShareDelete(w http.ResponseWriter, r *http.Request) {
	account, _ := requestProfile(r)
	fileMutationMu.Lock()
	_, err := db.Exec(`DELETE FROM upload_shares WHERE token=? AND EXISTS(SELECT 1 FROM users
		WHERE username=? AND session_version=? AND folder_access IS NULL AND
		(role='admin' OR (role='user' AND account_id=upload_shares.owner_id)))`,
		r.URL.Query().Get("token"), account.username, account.version)
	fileMutationMu.Unlock()
	if err != nil {
		logDBError("revoke upload link", err)
		http.Error(w, "upload link unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func loadUploadShare(token string) (uploadShare, int) {
	var link uploadShare
	var ownerAllowed bool
	var expires, received, maxFiles int64
	err := db.QueryRow(`SELECT source,path,folder_id,expires,max_bytes,max_files,received,conflict,
		EXISTS(SELECT 1 FROM users WHERE account_id=upload_shares.owner_id AND account_id!=''
		AND folder_access IS NULL AND role IN ('admin','user')) FROM upload_shares WHERE token=?`, token).
		Scan(&link.source, &link.path, &link.folderID, &expires, &link.maxBytes, &maxFiles, &received, &link.conflict, &ownerAllowed)
	if errors.Is(err, sql.ErrNoRows) {
		return link, http.StatusNotFound
	}
	if err != nil {
		return link, http.StatusServiceUnavailable
	}
	if !ownerAllowed || expires <= time.Now().Unix() || received >= maxFiles {
		return link, http.StatusGone
	}
	return link, http.StatusOK
}

func handleUploadSharePublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.PathValue("token")
	link, status := loadUploadShare(token)
	if status != http.StatusOK {
		http.Error(w, "upload link unavailable", status)
		return
	}
	folder, id, err := openReceiveFolder(link.source, link.path)
	if err != nil || id != link.folderID {
		if folder != nil {
			folder.Close()
		}
		http.Error(w, "upload link unavailable", http.StatusGone)
		return
	}
	defer folder.Close()
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodGet {
			receiveFileTemplate.Execute(w, struct {
				Russian  bool
				MaxMiB   int64
				MaxBytes int64
			}{strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "ru"), link.maxBytes / uploadShareMiB, link.maxBytes})
		}
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if status = receiveSharedFile(w, r, token, folder, link); status != http.StatusNoContent {
		http.Error(w, "file could not be accepted", status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func receiveSharedFile(w http.ResponseWriter, r *http.Request, token string, folder *os.File, link uploadShare) int {
	name, err := url.PathUnescape(r.Header.Get("X-Filehub-Filename"))
	if err != nil || badName(name) || len(name) > 255 || strings.ContainsFunc(name, unicode.IsControl) ||
		r.Header.Get("Content-Type") != "application/octet-stream" {
		return http.StatusBadRequest
	}
	if r.ContentLength < 0 {
		return http.StatusLengthRequired
	}
	limit := min(link.maxBytes, maxUploadBytes())
	if r.ContentLength > limit {
		return http.StatusRequestEntityTooLarge
	}
	// Reserve this link before reserving a global slot or reading any bytes.
	// One active transfer per link preserves capacity for other links.
	if _, busy := activeGuestLinks.LoadOrStore(token, struct{}{}); busy {
		return http.StatusServiceUnavailable
	}
	defer activeGuestLinks.Delete(token)
	select {
	case receiveUploadSlots <- struct{}{}:
		defer func() { <-receiveUploadSlots }()
	default:
		return http.StatusServiceUnavailable
	}
	if current, status := loadUploadShare(token); status != http.StatusOK {
		return status
	} else if current != link {
		return http.StatusGone
	}

	fd := int(folder.Fd())
	staged := ".filehub-receive-" + randToken()
	tmp, err := unix.Openat(fd, staged, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0644)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	defer unix.Unlinkat(fd, staged, 0)
	file := os.NewFile(uintptr(tmp), staged)
	controller := http.NewResponseController(w)
	defer controller.SetReadDeadline(time.Time{})
	input := guestUploadReader{http.MaxBytesReader(w, r.Body, limit), controller, time.Now().Add(2 * time.Hour)}
	written, transferErr := io.Copy(file, input)
	if transferErr == nil && written == r.ContentLength {
		transferErr = file.Sync()
	}
	closeErr := file.Close()
	var tooLarge *http.MaxBytesError
	if errors.As(transferErr, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	if errors.Is(transferErr, io.ErrUnexpectedEOF) || written != r.ContentLength || r.Context().Err() != nil {
		return http.StatusBadRequest
	}
	if transferErr != nil || closeErr != nil {
		return http.StatusServiceUnavailable
	}
	return finishSharedFile(r, token, folder, link, staged, name)
}

func finishSharedFile(r *http.Request, token string, folder *os.File, link uploadShare, staged, name string) int {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	if current, status := loadUploadShare(token); status != http.StatusOK {
		return status
	} else if current != link {
		return http.StatusGone
	}
	currentFolder, currentID, err := openReceiveFolder(link.source, link.path)
	if err != nil {
		return http.StatusGone
	}
	currentFolder.Close()
	if currentID != link.folderID || r.Context().Err() != nil {
		return http.StatusGone
	}

	// Reserve the file count before publication. A process failure between the two
	// can use a slot, but cannot publish more files than the owner allowed.
	claimed, err := db.Exec(`UPDATE upload_shares SET received=received+1 WHERE token=? AND expires>? AND received<max_files`,
		token, time.Now().Unix())
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if changed, err := claimed.RowsAffected(); err != nil || changed != 1 {
		return http.StatusGone
	}

	fd := int(folder.Fd())
	status := http.StatusConflict
	for attempt := 0; attempt < uploadNameAttempts; attempt++ {
		candidate := uploadCandidateName(name, attempt)
		err := unix.Renameat2(fd, staged, fd, candidate, unix.RENAME_NOREPLACE)
		if err == nil {
			if err := folder.Sync(); err != nil {
				return http.StatusServiceUnavailable
			}
			rel := joinRel(link.path, candidate)
			if full, ok := safePath(link.source, rel); ok {
				logDBError("index received file", indexAddPath(link.source, full, rel))
				logDBError("queue received file", queueMedia(link.source, rel))
			}
			return http.StatusNoContent
		}
		if !errors.Is(err, unix.EEXIST) {
			status = http.StatusServiceUnavailable
			break
		}
		if link.conflict == "reject" {
			break
		}
	}
	_, err = db.Exec(`UPDATE upload_shares SET received=received-1 WHERE token=? AND received>0`, token)
	logDBError("release upload link slot", err)
	return status
}
