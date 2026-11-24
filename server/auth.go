// Multi-user auth: users table (bcrypt), roles admin/user/viewer, signed-cookie
// session carrying the username. Bootstraps an `admin` user from FILEHUB_PASSWORD.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var authSecret []byte
var dummyPasswordHash []byte

const (
	minPasswordLength  = 12
	maxSmallFormBytes  = 64 << 10
	maxLoginFailures   = 5
	maxAccountFailures = 20
	loginWindow        = 15 * time.Minute
)

var passwordCheckSlots = make(chan struct{}, 4)

type loginAttempt struct {
	Failures     int
	First        time.Time
	BlockedUntil time.Time
}

var (
	loginAttemptsMu sync.Mutex
	loginAttempts   = map[string]loginAttempt{}
)

func initAuth() {
	if len(dummyPasswordHash) == 0 {
		dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("filehub-invalid-login"), bcrypt.DefaultCost)
	}
	if s := env("FILEHUB_SECRET", ""); s != "" {
		authSecret = []byte(s)
		return
	}
	// No env secret → persist a generated one so a restart doesn't log everyone out.
	path := env("FILEHUB_SECRET_FILE", "/opt/filehub/secret")
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		authSecret = b
		return
	}
	authSecret = make([]byte, 32)
	if _, err := rand.Read(authSecret); err != nil {
		log.Fatalf("filehub: generate session secret: %v", err)
	}
	if err := os.WriteFile(path, authSecret, 0o600); err != nil {
		log.Fatalf("filehub: persist session secret: %v", err)
	}
}

// initUsers creates the users table and bootstraps an admin from FILEHUB_PASSWORD.
func initUsers() error {
	if db == nil {
		return fmt.Errorf("db not ready")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS users(
		username TEXT PRIMARY KEY, pwhash TEXT, role TEXT, created INTEGER)`); err != nil {
		return err
	}
	if err := initSessionVersions(); err != nil {
		return err
	}
	if err := initRevokedSessions(); err != nil {
		return err
	}
	if err := initFolderAccess(); err != nil {
		return err
	}
	if err := initInvitations(); err != nil {
		return err
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		pw := os.Getenv("FILEHUB_PASSWORD")
		if len(pw) < minPasswordLength || pw == "admin" {
			return fmt.Errorf("FILEHUB_PASSWORD must be set to at least %d characters for initial bootstrap", minPasswordLength)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		version, err := newSessionVersion()
		if err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO users(username,pwhash,role,created,session_version) VALUES('admin',?,'admin',?,?)`, string(h), time.Now().Unix(), version); err != nil {
			return err
		}
		log.Printf("filehub: bootstrapped admin user (login 'admin', password from FILEHUB_PASSWORD)")
	}
	return nil
}

func signSession(username string) string {
	version, err := userSessionVersion(username)
	if err != nil {
		return ""
	}
	return signSessionVersion(username, version)
}

func signSessionVersion(username, version string) string {
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	msg := username + "|" + strconv.FormatInt(exp, 10) + "|" + version + "|" + randToken()
	mac := hmac.New(sha256.New, authSecret)
	mac.Write([]byte(msg))
	return msg + "|" + hex.EncodeToString(mac.Sum(nil))
}

func parseSession(tok string) (string, bool) {
	p := strings.Split(tok, "|")
	if (len(p) < 3 || len(p) > 5) || db == nil {
		return "", false
	}
	exp, err := strconv.ParseInt(p[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	mac := hmac.New(sha256.New, authSecret)
	mac.Write([]byte(strings.Join(p[:len(p)-1], "|")))
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(p[len(p)-1])) {
		return "", false
	}
	version := ""
	if len(p) >= 4 {
		version = p[2]
	}
	var current string
	err = db.QueryRow(`SELECT session_version FROM users WHERE username=? AND NOT EXISTS
		(SELECT 1 FROM revoked_sessions WHERE token_hash=?)`, p[0], sha256hex(tok)).Scan(&current)
	if err != nil || version != current {
		return "", false
	}
	return p[0], true
}

func userRole(username string) string {
	if db == nil {
		return ""
	}
	var role string
	db.QueryRow(`SELECT role FROM users WHERE username=?`, username).Scan(&role)
	return role
}

// currentUser returns (username, role, ok) from the session cookie.
func currentUser(r *http.Request) (string, string, bool) {
	account, ok := requestProfile(r)
	return account.username, account.role, ok
}

func sameOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true // native apps, WebDAV clients and CLI callers do not send Origin
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}

func mutationAllowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func parseSmallForm(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength > maxSmallFormBytes {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSmallFormBytes)
	if err := r.ParseForm(); err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "request body too large") {
			code = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "bad form", code)
		return false
	}
	return true
}

func trustedProxy(host string) bool {
	peer := net.ParseIP(host)
	for _, cidr := range strings.Split(os.Getenv("FILEHUB_TRUSTED_PROXIES"), ",") {
		_, network, parseErr := net.ParseCIDR(strings.TrimSpace(cidr))
		if parseErr == nil && peer != nil && network.Contains(peer) {
			return true
		}
	}
	return false
}

func requestHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	values := r.Header.Values("X-Forwarded-Proto")
	return err == nil && trustedProxy(host) && len(values) == 1 && values[0] == "https"
}

func loginKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if trustedProxy(host) {
		values := r.Header.Values("X-Real-IP")
		if len(values) == 1 {
			if client := net.ParseIP(strings.TrimSpace(values[0])); client != nil {
				return client.String()
			}
		}
	}
	return host
}

func loginAllowed(key string) bool {
	now := time.Now()
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	a, ok := loginAttempts[key]
	if !ok {
		return true
	}
	if !a.BlockedUntil.IsZero() {
		if now.Before(a.BlockedUntil) {
			return false
		}
		delete(loginAttempts, key)
		return true
	}
	if now.Sub(a.First) >= loginWindow {
		delete(loginAttempts, key)
		return true
	}
	return a.BlockedUntil.IsZero()
}

func recordLoginFailure(key string) {
	recordLoginFailureLimit(key, maxLoginFailures)
}

func recordLoginFailureLimit(key string, limit int) {
	now := time.Now()
	loginAttemptsMu.Lock()
	defer loginAttemptsMu.Unlock()
	a, ok := loginAttempts[key]
	if ok && now.Before(a.BlockedUntil) {
		return // Concurrent failures cannot extend an already active block.
	}
	if !ok || now.Sub(a.First) > loginWindow {
		a = loginAttempt{First: now}
	}
	a.Failures++
	if a.Failures >= limit {
		a.BlockedUntil = now.Add(loginWindow)
	}
	loginAttempts[key] = a
}

func accountLoginKey(channel, username string) string {
	if !validUsername(username) {
		return ""
	}
	return channel + ":account:" + username
}

func passwordRetryAfter(source, account string) string {
	if !loginAllowed(source) {
		return loginRetryAfter(source)
	}
	if account != "" && !loginAllowed(account) {
		return loginRetryAfter(account)
	}
	return ""
}

func recordPasswordFailure(source, account string) {
	recordLoginFailure(source)
	if account != "" {
		recordLoginFailureLimit(account, maxAccountFailures)
	}
}

func clearLoginFailures(key string) {
	loginAttemptsMu.Lock()
	delete(loginAttempts, key)
	loginAttemptsMu.Unlock()
}

func loginRetryAfter(key string) string {
	loginAttemptsMu.Lock()
	remaining := time.Until(loginAttempts[key].BlockedUntil)
	loginAttemptsMu.Unlock()
	seconds := int((remaining + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func setSession(w http.ResponseWriter, r *http.Request, username, version string) {
	http.SetCookie(w, &http.Cookie{
		Name: "fh_session", Value: signSessionVersion(username, version), Path: "/",
		HttpOnly: true, Secure: requestHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
	})
}

var loginLog = struct {
	sync.Mutex
	last map[int]time.Time
}{last: make(map[int]time.Time)}

// The journal records the failure category without retaining credentials,
// hostnames, or IP addresses. Bound duplicate events during retry storms.
func logLoginRejection(status int) {
	now := time.Now()
	loginLog.Lock()
	defer loginLog.Unlock()
	if now.Sub(loginLog.last[status]) >= 5*time.Second {
		log.Printf("filehub: login rejected http_status=%d", status)
		loginLog.last[status] = now
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		logLoginRejection(http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	key := loginKey(r)
	accountKey := accountLoginKey("web", username)
	if retry := passwordRetryAfter(key, accountKey); retry != "" {
		logLoginRejection(http.StatusTooManyRequests)
		w.Header().Set("Retry-After", retry)
		http.Error(w, "слишком много попыток, попробуй позже", http.StatusTooManyRequests)
		return
	}
	select {
	case passwordCheckSlots <- struct{}{}:
		defer func() { <-passwordCheckSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "login temporarily busy", http.StatusTooManyRequests)
		return
	}
	var pwhash, role, version string
	var folderAccess sql.NullString
	dbErr := db.QueryRowContext(r.Context(), `SELECT pwhash,role,session_version,folder_access FROM users WHERE username=?`, username).
		Scan(&pwhash, &role, &version, &folderAccess)
	if dbErr != nil && !errors.Is(dbErr, sql.ErrNoRows) {
		logLoginRejection(http.StatusServiceUnavailable)
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	candidate := []byte(pwhash)
	if dbErr != nil {
		candidate = dummyPasswordHash
	}
	passwordErr := bcrypt.CompareHashAndPassword(candidate, []byte(password))
	if dbErr != nil || passwordErr != nil {
		recordPasswordFailure(key, accountKey)
		logLoginRejection(http.StatusUnauthorized)
		http.Error(w, "неверный логин или пароль", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// Sign the version whose password was verified. A concurrent reset makes
	// this cookie invalid instead of authorizing the old password's caller.
	setSession(w, r, username, version)
	var personal *folderGrant
	if folderAccess.Valid {
		if policy, err := decodeFolderAccess(folderAccess.String); err != nil || policy.Personal == nil {
			role = "viewer"
		} else {
			personal = policy.Personal
		}
	}
	writeJSON(w, map[string]any{"ok": true, "username": username, "role": role, "restricted": folderAccess.Valid, "personal": personal})
}

// handleSelfPassword lets any logged-in user change their OWN password after
// re-entering the current one (admin password reset stays separate).
func handleSelfPassword(w http.ResponseWriter, r *http.Request) {
	username, _, ok := currentUser(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !parseSmallForm(w, r) {
		return
	}
	cur, nw := r.PostFormValue("current"), r.PostFormValue("new")
	var pwhash, version string
	if db.QueryRow(`SELECT pwhash,session_version FROM users WHERE username=?`, username).Scan(&pwhash, &version) != nil {
		http.Error(w, "нет такого пользователя", http.StatusNotFound)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(pwhash), []byte(cur)) != nil {
		http.Error(w, "неверный текущий пароль", http.StatusForbidden)
		return
	}
	if len(nw) < minPasswordLength {
		http.Error(w, fmt.Sprintf("новый пароль минимум %d символов", minPasswordLength), http.StatusBadRequest)
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(nw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "не удалось изменить пароль", http.StatusBadRequest)
		return
	}
	nextVersion, err := newSessionVersion()
	if err != nil {
		http.Error(w, "не удалось изменить пароль", http.StatusInternalServerError)
		return
	}
	result, err := db.Exec(`UPDATE users SET pwhash=?,session_version=? WHERE username=? AND pwhash=? AND session_version=?`, string(h), nextVersion, username, pwhash, version)
	if err != nil {
		http.Error(w, "не удалось изменить пароль", http.StatusInternalServerError)
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		http.Error(w, "пароль уже изменён, войди заново", http.StatusConflict)
		return
	}
	davInvalidate(username)
	w.Header().Set("Cache-Control", "no-store")
	setSession(w, r, username, nextVersion)
	writeJSON(w, map[string]any{"ok": true})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("fh_session")
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(cookie.Value, "|")
	if _, ok := parseSession(cookie.Value); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	expires, _ := strconv.ParseInt(parts[1], 10, 64)
	if _, err := db.Exec(`INSERT OR IGNORE INTO revoked_sessions(token_hash,expires) VALUES(?,?)`, sha256hex(cookie.Value), expires); err != nil {
		logDBError("revoke session", err)
		http.Error(w, "could not revoke session", http.StatusServiceUnavailable)
		return
	}
	_, err = db.Exec(`DELETE FROM revoked_sessions WHERE expires<?`, time.Now().Unix())
	logDBError("expire revoked sessions", err)
	http.SetCookie(w, &http.Cookie{Name: "fh_session", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: requestHTTPS(r), SameSite: http.SameSiteLaxMode})
	writeJSON(w, map[string]any{"ok": true})
}

func handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	account, ok := requestProfile(r)
	w.Header().Set("Cache-Control", "no-store")
	if !ok {
		writeJSON(w, map[string]any{"authed": false, "username": "", "role": ""})
		return
	}
	var personal *folderGrant
	if account.folders != nil {
		personal = account.folders.Personal
	}
	writeJSON(w, map[string]any{"authed": true, "username": account.username, "role": account.role,
		"restricted": account.folders != nil, "personal": personal})
}

func requireMutation(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !mutationAllowed(w, r) {
			return
		}
		h(w, r)
	}
}

func requireAuthMutation(h http.HandlerFunc) http.HandlerFunc {
	return requireMutation(requireAuth(h))
}

func requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return authenticatedHandler(h, false, false)
}

func requireWriteMutation(h http.HandlerFunc) http.HandlerFunc {
	return requireMutation(requireWrite(h))
}

func requireWrite(h http.HandlerFunc) http.HandlerFunc {
	return authenticatedHandler(h, false, true)
}

// Handlers must opt in to restricted accounts and then enforce the scope on
// file paths or on every metadata result they return.
func authenticatedHandler(h http.HandlerFunc, allowFolders, write bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		account, ok := requestProfile(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if (!allowFolders && account.folders != nil) || (write && account.role == "viewer") {
			http.Error(w, "недоступно для этого профиля", http.StatusForbidden)
			return
		}
		if account.folders != nil {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		h(w, withProfile(r, account))
	}
}

func requireAdminMutation(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !mutationAllowed(w, r) {
			return
		}
		if _, role, ok := currentUser(r); !ok || role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, role, ok := currentUser(r); !ok || role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func securityMiddleware(next http.Handler) http.Handler {
	requireHTTPS := os.Getenv("FILEHUB_REQUIRE_HTTPS") == "1"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requireHTTPS {
			if !requestHTTPS(r) {
				http.Error(w, "HTTPS required", http.StatusUpgradeRequired)
				return
			}
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if unsafe && !strings.HasPrefix(r.URL.Path, "/dav/") && !sameOrigin(r) {
			if r.URL.Path == "/api/login" {
				logLoginRejection(http.StatusForbidden)
			}
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Public app documents and map workers must identify their origin to tile
// providers. Cross-origin requests expose only the origin, never a file path,
// search query or share token. Private API/share responses retain no-referrer.
func webReferrerPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
