// Public share links: token -> file/folder, with optional password and expiry.
// Stored in the SQLite db (shared with search). /s/<token> is public.
package main

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sys/unix"
)

var sharePasswordSlots = make(chan struct{}, 2)

func initShares() error {
	if err := initUploadShares(); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS shares(
		token TEXT PRIMARY KEY, source TEXT, path TEXT, isdir INTEGER,
		pwhash TEXT, expires INTEGER, created INTEGER, owner_id TEXT NOT NULL DEFAULT '');`)
	if err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('shares') WHERE name='owner_id'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(`ALTER TABLE shares ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('shares') WHERE name='object_id'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(`ALTER TABLE shares ADD COLUMN object_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE TRIGGER IF NOT EXISTS filehub_revoke_restricted_shares AFTER UPDATE OF role,folder_access ON users
		WHEN NEW.folder_access IS NOT NULL OR NEW.role NOT IN ('admin','user') BEGIN
			UPDATE shares SET expires=1 WHERE owner_id=OLD.account_id AND owner_id!='';
		END;
		CREATE TRIGGER IF NOT EXISTS filehub_revoke_deleted_shares AFTER DELETE ON users BEGIN
			UPDATE shares SET expires=1 WHERE owner_id=OLD.account_id AND owner_id!='';
		END;`); err != nil {
		return err
	}
	return tx.Commit()
}

func registerShares(mux *http.ServeMux) {
	mux.HandleFunc("/api/share/create", requireWriteMutation(handleShareCreate))
	mux.HandleFunc("/api/share/create-upload", requireWriteMutation(handleUploadShareCreate))
	mux.HandleFunc("/api/share/list", requireAuth(handleShareList))
	mux.HandleFunc("/api/share/delete", requireWriteMutation(handleShareDelete))
	mux.HandleFunc("/s/{token}", handleSharePublic)
	mux.HandleFunc("/u/{token}", handleUploadSharePublic)
}

func randToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func openSharedObject(source, path string, isDir bool) (*os.File, string, error) {
	full, root, ok := resolveItem(source, path)
	if !ok {
		return nil, "", os.ErrInvalid
	}
	relative, err := filepath.Rel(root, full)
	if err != nil {
		return nil, "", err
	}
	var f *os.File
	if isDir {
		if relative == "." {
			relative = ""
		}
		f, err = openPublicDirectory(root, relative)
	} else {
		f, err = openArchiveFile(root, relative)
	}
	return f, full, err
}

func sharedObjectID(f *os.File) (string, error) {
	var identity unix.Statx_t
	err := unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_INO|unix.STATX_BTIME, &identity)
	if err == nil && identity.Mask&(unix.STATX_INO|unix.STATX_BTIME) == unix.STATX_INO|unix.STATX_BTIME {
		return fmt.Sprintf("birth:%d:%d:%d:%d:%d", identity.Dev_major, identity.Dev_minor, identity.Ino, identity.Btime.Sec, identity.Btime.Nsec), nil
	}
	var fallback unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &fallback); err != nil {
		return "", err
	}
	return fmt.Sprintf("change:%d:%d:%d:%d", fallback.Dev, fallback.Ino, fallback.Ctim.Sec, fallback.Ctim.Nsec), nil
}

func handleShareCreate(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	source, path := r.PostFormValue("source"), r.PostFormValue("path")
	full, _, ok := resolveItem(source, path)
	if !ok {
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	isdir := 0
	if st.IsDir() {
		isdir = 1
	}
	file, _, err := openSharedObject(source, path, isdir == 1)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	objectID, err := sharedObjectID(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var pwhash string
	if pw := r.PostFormValue("password"); pw != "" {
		if len([]byte(pw)) > 72 {
			http.Error(w, "пароль ссылки слишком длинный", http.StatusBadRequest)
			return
		}
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "password error", http.StatusBadRequest)
			return
		}
		pwhash = string(h)
	}
	var expires int64
	if n, _ := strconv.Atoi(r.PostFormValue("days")); n > 0 {
		expires = time.Now().AddDate(0, 0, n).Unix()
	}
	token := randToken()
	account, _ := requestProfile(r)
	result, err := db.Exec(`INSERT INTO shares(token,source,path,isdir,pwhash,expires,created,object_id,owner_id)
		SELECT ?,?,?,?,?,?,?,?,account_id FROM users
		WHERE username=? AND session_version=? AND folder_access IS NULL AND role IN ('admin','user') AND account_id!=''`,
		token, source, path, isdir, pwhash, expires, time.Now().Unix(), objectID, account.username, account.version)
	if err != nil {
		logDBError("create public link", err)
		http.Error(w, "could not create link", http.StatusServiceUnavailable)
		return
	}
	if created, err := result.RowsAffected(); err != nil || created != 1 {
		http.Error(w, "link access changed", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"token": token, "url": "/s/" + token})
}

func handleShareList(w http.ResponseWriter, r *http.Request) {
	user, role, _ := currentUser(r)
	w.Header().Set("Cache-Control", "no-store")
	rows, err := db.Query(`SELECT token,source,path,isdir,expires,created,(pwhash!=''),'','',0,0,0 FROM shares
		WHERE owner_id=(SELECT account_id FROM users WHERE username=? AND role IN ('admin','user')) OR ?='admin'
		UNION ALL SELECT token,source,path,1,expires,created,0,'upload',conflict,max_bytes,max_files,received
		FROM upload_shares WHERE owner_id=(SELECT account_id FROM users WHERE username=? AND role IN ('admin','user')) OR ?='admin'
		ORDER BY created DESC`, user, role, user, role)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type S struct {
		Token    string `json:"token"`
		Source   string `json:"source"`
		Path     string `json:"path"`
		IsDir    bool   `json:"isDir"`
		Expires  int64  `json:"expires"`
		Created  int64  `json:"created"`
		HasPw    bool   `json:"hasPassword"`
		Kind     string `json:"kind,omitempty"`
		Conflict string `json:"conflict,omitempty"`
		MaxBytes int64  `json:"maxBytes,omitempty"`
		MaxFiles int    `json:"maxFiles,omitempty"`
		Received int    `json:"received,omitempty"`
	}
	out := []S{}
	for rows.Next() {
		var s S
		var isdir, haspw int64
		if rows.Scan(&s.Token, &s.Source, &s.Path, &isdir, &s.Expires, &s.Created, &haspw,
			&s.Kind, &s.Conflict, &s.MaxBytes, &s.MaxFiles, &s.Received) == nil {
			s.IsDir, s.HasPw = isdir == 1, haspw == 1
			out = append(out, s)
		}
	}
	writeJSON(w, map[string]any{"shares": out})
}

func handleShareDelete(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("kind") == "upload" {
		handleUploadShareDelete(w, r)
		return
	}
	account, _ := requestProfile(r)
	if _, err := db.Exec(`DELETE FROM shares WHERE token=? AND EXISTS(SELECT 1 FROM users
		WHERE username=? AND session_version=? AND folder_access IS NULL AND
		(role='admin' OR (role='user' AND account_id=shares.owner_id)))`,
		r.URL.Query().Get("token"), account.username, account.version); err != nil {
		logDBError("revoke public link", err)
		http.Error(w, "could not revoke link", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func sharePasswordOK(stored, password string) bool {
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
	want, got := []byte(stored), []byte(sha256hex(password))
	return len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
}

func pwForm(w http.ResponseWriter, token string, wrong bool) {
	msg := ""
	if wrong {
		msg = `<p style="color:#ff6b6b">Неверный пароль</p>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>filehub · ссылка</title>
<body style="font:15px system-ui;background:#0f1115;color:#e6e9ef;display:flex;height:100vh;margin:0;align-items:center;justify-content:center">
<form method=post style="background:#171a21;padding:28px;border-radius:12px;border:1px solid #2a2f3a;min-width:280px">
<h3 style="margin:0 0 14px">🔒 Ссылка защищена паролем</h3>` + msg + `
<input name=pw type=password placeholder=Пароль autofocus style="width:100%;padding:9px;border-radius:7px;border:1px solid #2a2f3a;background:#0f1115;color:#e6e9ef;margin-bottom:12px">
<button style="width:100%;padding:9px;border:0;border-radius:7px;background:#4f8cff;color:#fff;cursor:pointer">Открыть</button>
</form></body>`))
}

func authorizeSharePassword(w http.ResponseWriter, r *http.Request, token, stored string) bool {
	source := "share:source:" + loginKey(r)
	link := "share:token:" + sha256hex(token)
	if retry := passwordRetryAfter(source, link); retry != "" {
		w.Header().Set("Retry-After", retry)
		http.Error(w, "too many password attempts", http.StatusTooManyRequests)
		return false
	}
	if !parseSmallForm(w, r) {
		return false
	}
	password := r.PostFormValue("pw")
	if password == "" {
		pwForm(w, token, false)
		return false
	}
	select {
	case sharePasswordSlots <- struct{}{}:
		defer func() { <-sharePasswordSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "password verification busy", http.StatusTooManyRequests)
		return false
	}
	if !sharePasswordOK(stored, password) {
		recordPasswordFailure(source, link)
		pwForm(w, token, true)
		return false
	}
	if !strings.HasPrefix(stored, "$2") {
		if hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost); err == nil {
			_, err = db.Exec(`UPDATE shares SET pwhash=? WHERE token=? AND pwhash=?`, string(hash), token, stored)
			logDBError("upgrade link password hash", err)
		}
	}
	return true
}

// handleSharePublic serves a shared item (file download or folder zip) — no auth.
func handleSharePublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	token := r.PathValue("token")
	var source, path, pwhash, objectID string
	var isdir, expires int64
	err := db.QueryRow(`SELECT source,path,isdir,pwhash,expires,object_id FROM shares WHERE token=?`, token).
		Scan(&source, &path, &isdir, &pwhash, &expires, &objectID)
	if err != nil {
		http.Error(w, "ссылка не найдена", http.StatusNotFound)
		return
	}
	if expires != 0 && time.Now().Unix() > expires {
		http.Error(w, "срок ссылки истёк", http.StatusGone)
		return
	}
	if pwhash != "" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			pwForm(w, token, false)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorizeSharePassword(w, r, token, pwhash) {
			return
		}
	}
	f, full, err := openSharedObject(source, path, isdir == 1)
	if err != nil {
		http.Error(w, "shared object unavailable", http.StatusGone)
		return
	}
	defer f.Close()
	if current, err := sharedObjectID(f); err != nil || (objectID != "" && current != objectID) {
		http.Error(w, "shared object changed", http.StatusGone)
		return
	}
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "shared object unavailable", http.StatusGone)
		return
	}
	if isdir == 1 {
		name := filepath.Base(full)
		if name == "." || name == "/" || name == "" {
			name = "share"
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}))
		zw := zip.NewWriter(w)
		defer zw.Close()
		pinned := fmt.Sprintf("/proc/self/fd/%d", f.Fd())
		addArchiveTree(zw, pinned, pinned+"/.", name)
		return
	}
	originalHeaders(w, full, false)
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}
