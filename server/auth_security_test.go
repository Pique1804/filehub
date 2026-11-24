package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const authTestOldPassword = "fixture-old-password"
const authTestNewPassword = "fixture-new-password"

func authSecurityFixture(t *testing.T) http.Handler {
	t.Helper()
	oldDB, oldSecret, oldDummy := db, authSecret, dummyPasswordHash
	oldSources, oldByName := sources, byName
	var err error
	db, err = sql.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	testDB := db
	authSecret = []byte("isolated-auth-security-test-secret")
	dummyPasswordHash, err = bcrypt.GenerateFromPassword([]byte(authTestOldPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE users(username TEXT PRIMARY KEY,pwhash TEXT,role TEXT,created INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	for user, role := range map[string]string{"admin": "admin", "writer": "user", "other": "user", "reader": "viewer"} {
		if _, err := db.Exec(`INSERT INTO users VALUES(?,?,?,?)`, user, string(dummyPasswordHash), role, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := initUsers(); err != nil {
		t.Fatal(err)
	}
	if err := initUsers(); err != nil {
		t.Fatal("migration not idempotent:", err)
	}
	sources = []Source{{Name: "T", Root: t.TempDir()}}
	byName = map[string]string{"T": sources[0].Root}
	loginAttempts = map[string]loginAttempt{}
	davCache.Range(func(key, _ any) bool { davCache.Delete(key); return true })
	t.Cleanup(func() {
		testDB.Close()
		db, authSecret, dummyPasswordHash = oldDB, oldSecret, oldDummy
		sources, byName = oldSources, oldByName
		loginAttempts = map[string]loginAttempt{}
		davCache.Range(func(key, _ any) bool { davCache.Delete(key); return true })
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", requireMutation(handleLogin))
	mux.HandleFunc("/api/logout", requireBrowseMutation(handleLogout))
	mux.HandleFunc("/api/password", requireAuthMutation(handleSelfPassword))
	mux.HandleFunc("/api/admin/user/password", requireAdminMutation(handleUserPassword))
	mux.HandleFunc("/api/admin/user/create", requireAdminMutation(handleUserCreate))
	mux.HandleFunc("/api/admin/user/delete", requireAdminMutation(handleUserDelete))
	mux.HandleFunc("/api/admin/user/role", requireAdminMutation(handleUserRole))
	mux.HandleFunc("/protected/read", requireAuth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	mux.HandleFunc("/protected/write", requireWriteMutation(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	mux.Handle("/dav/", davHandler())
	return mux
}

func TestLogoutRevokesOnlyTheSelectedSession(t *testing.T) {
	h := authSecurityFixture(t)
	first, second := signSession("writer"), signSession("writer")
	if first == "" || first == second {
		t.Fatal("sessions must be distinct")
	}
	if r := securityRequest(h, "POST", "/api/logout", first, nil); r.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", r.Code, r.Body)
	}
	if r := securityRequest(h, "GET", "/protected/read", first, nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted: %d", r.Code)
	}
	if r := securityRequest(h, "GET", "/protected/read", second, nil); r.Code != http.StatusOK {
		t.Fatalf("other session revoked: %d", r.Code)
	}
}

func TestValidLoginDoesNotResetOtherPasswordAttempts(t *testing.T) {
	h := authSecurityFixture(t)
	wrong := url.Values{"username": {"admin"}, "password": {"fixture-incorrect"}}
	for i := 0; i < maxLoginFailures-1; i++ {
		if r := securityRequest(h, "POST", "/api/login", "", wrong); r.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected login result: %d", r.Code)
		}
	}
	securityLogin(t, h, "writer", authTestOldPassword)
	if r := securityRequest(h, "POST", "/api/login", "", wrong); r.Code != http.StatusUnauthorized {
		t.Fatalf("last failed attempt: %d", r.Code)
	}
	if r := securityRequest(h, "POST", "/api/login", "", wrong); r.Code != http.StatusTooManyRequests || r.Header().Get("Retry-After") == "" {
		t.Fatalf("source budget reset: %d", r.Code)
	}
}

func legacyTestSession(user string) string {
	msg := user + "|" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	mac := hmac.New(sha256.New, authSecret)
	mac.Write([]byte(msg))
	return msg + "|" + hex.EncodeToString(mac.Sum(nil))
}

func securityRequest(h http.Handler, method, path, cookie string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.RemoteAddr = "192.0.2.10:4567"
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "fh_session", Value: cookie})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func securityLogin(t *testing.T, h http.Handler, user, password string) string {
	t.Helper()
	r := securityRequest(h, "POST", "/api/login", "", url.Values{"username": {user}, "password": {password}})
	if r.Code != 200 || len(r.Result().Cookies()) != 1 {
		t.Fatalf("login status %d", r.Code)
	}
	return r.Result().Cookies()[0].Value
}

func TestConcurrentProfileChangesKeepAnAdministrator(t *testing.T) {
	h := authSecurityFixture(t)
	db.SetMaxOpenConns(1)
	admin := securityLogin(t, h, "admin", authTestOldPassword)
	writer := securityLogin(t, h, "writer", authTestOldPassword)
	if got := securityRequest(h, "POST", "/api/admin/user/role?username=writer&role=admin", admin, nil).Code; got != http.StatusOK {
		t.Fatalf("could not create second administrator: %d", got)
	}

	start := make(chan struct{})
	results := make(chan int, 2)
	go func() {
		<-start
		results <- securityRequest(h, "POST", "/api/admin/user/delete?username=admin", admin, nil).Code
	}()
	go func() {
		<-start
		results <- securityRequest(h, "POST", "/api/admin/user/role?username=writer&role=viewer", writer, nil).Code
	}()
	close(start)
	first, second := <-results, <-results
	if !((first == http.StatusOK && second == http.StatusBadRequest) ||
		(second == http.StatusOK && first == http.StatusBadRequest)) {
		t.Fatalf("profile mutations returned %d and %d", first, second)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM users WHERE role='admin'`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("wanted one remaining administrator, got %d (%v)", remaining, err)
	}
}

func TestAuthLegacyMigrationAndCookieIntegrity(t *testing.T) {
	h := authSecurityFixture(t)
	legacy, fresh := legacyTestSession("writer"), securityLogin(t, h, "writer", authTestOldPassword)
	for _, cookie := range []string{legacy, fresh} {
		if got := securityRequest(h, "GET", "/protected/read", cookie, nil).Code; got != 200 {
			t.Fatalf("compatible cookie rejected: %d", got)
		}
	}
	for _, cookie := range []string{fresh + "x", strings.Replace(fresh, "writer", "admin", 1), "writer|0|bad", "bad"} {
		if got := securityRequest(h, "GET", "/protected/read", cookie, nil).Code; got != 401 {
			t.Fatalf("invalid cookie accepted: %d", got)
		}
	}
}

func TestAuthSelfPasswordRevokesOnlyChangedUser(t *testing.T) {
	h := authSecurityFixture(t)
	old := securityLogin(t, h, "writer", authTestOldPassword)
	other := securityLogin(t, h, "other", authTestOldPassword)
	legacy := legacyTestSession("writer")
	r := securityRequest(h, "POST", "/api/password", old, url.Values{"current": {authTestOldPassword}, "new": {authTestNewPassword}})
	if r.Code != 200 || len(r.Result().Cookies()) != 1 {
		t.Fatalf("password status %d", r.Code)
	}
	fresh := r.Result().Cookies()[0].Value
	for _, cookie := range []string{old, legacy} {
		if got := securityRequest(h, "GET", "/protected/read", cookie, nil).Code; got != 401 {
			t.Fatalf("old password session accepted: %d", got)
		}
	}
	for _, cookie := range []string{fresh, other} {
		if got := securityRequest(h, "GET", "/protected/read", cookie, nil).Code; got != 200 {
			t.Fatalf("current/unrelated session rejected: %d", got)
		}
	}
	if r := securityRequest(h, "POST", "/api/login", "", url.Values{"username": {"writer"}, "password": {authTestOldPassword}}); r.Code != 401 {
		t.Fatalf("old password login accepted: %d", r.Code)
	}
	securityLogin(t, h, "writer", authTestNewPassword)
}

func TestAuthAdminResetRevokesAllOldCookies(t *testing.T) {
	h := authSecurityFixture(t)
	admin := securityLogin(t, h, "admin", authTestOldPassword)
	old := securityLogin(t, h, "writer", authTestOldPassword)
	r := securityRequest(h, "POST", "/api/admin/user/password", admin, url.Values{"username": {"writer"}, "password": {authTestNewPassword}})
	if r.Code != 200 {
		t.Fatalf("admin reset status %d", r.Code)
	}
	for _, cookie := range []string{old, legacyTestSession("writer"), signSessionVersion("writer", "")} {
		if got := securityRequest(h, "GET", "/protected/read", cookie, nil).Code; got != 401 {
			t.Fatalf("reset session accepted: %d", got)
		}
	}
	securityLogin(t, h, "writer", authTestNewPassword)
}

func TestAuthFailedChangeLeavesSessionsValid(t *testing.T) {
	h := authSecurityFixture(t)
	old := securityLogin(t, h, "writer", authTestOldPassword)
	for _, form := range []url.Values{{"current": {"wrong"}, "new": {authTestNewPassword}}, {"current": {authTestOldPassword}, "new": {"short"}}} {
		if got := securityRequest(h, "POST", "/api/password", old, form).Code; got == 200 {
			t.Fatal("invalid password change accepted")
		}
		if got := securityRequest(h, "GET", "/protected/read", old, nil).Code; got != 200 {
			t.Fatalf("failed change revoked session: %d", got)
		}
	}
}

func TestAuthDeletedUsernameCannotResurrectSession(t *testing.T) {
	h := authSecurityFixture(t)
	admin := securityLogin(t, h, "admin", authTestOldPassword)
	old, legacy := signSession("writer"), legacyTestSession("writer")
	if got := securityRequest(h, "POST", "/api/admin/user/delete?username=writer", admin, nil).Code; got != 200 {
		t.Fatalf("delete status %d", got)
	}
	if got := securityRequest(h, "POST", "/api/admin/user/create", admin, url.Values{"username": {"writer"}, "password": {authTestOldPassword}, "role": {"user"}}).Code; got != 200 {
		t.Fatalf("recreate status %d", got)
	}
	for _, cookie := range []string{old, legacy} {
		if got := securityRequest(h, "GET", "/protected/read", cookie, nil).Code; got != 401 {
			t.Fatalf("recreated user accepted old session: %d", got)
		}
	}
	securityLogin(t, h, "writer", authTestOldPassword)
}

func davSecurityRequest(h http.Handler, user, password, method string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/dav/", nil)
	req.RemoteAddr = "192.0.2.10:4567"
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	req.Header.Set("Depth", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestDAVAnonymousProbesDoNotConsumeFailures(t *testing.T) {
	h := authSecurityFixture(t)
	for i := 0; i < 12; i++ {
		for _, method := range []string{"OPTIONS", "PROPFIND"} {
			r := davSecurityRequest(h, "", "", method)
			if r.Code != 401 || r.Header().Get("DAV") == "" || r.Header().Get("WWW-Authenticate") == "" {
				t.Fatalf("capability challenge changed: %d", r.Code)
			}
		}
	}
	if len(loginAttempts) != 0 {
		t.Fatal("anonymous probes counted as failed passwords")
	}
	if got := davSecurityRequest(h, "writer", authTestOldPassword, "PROPFIND").Code; got != 207 {
		t.Fatalf("valid PROPFIND failed: %d", got)
	}
}

func TestDAVFailuresBlockOnlyDAVAndPreserveCachedClient(t *testing.T) {
	h := authSecurityFixture(t)
	if got := davSecurityRequest(h, "writer", authTestOldPassword, "OPTIONS").Code; got != 200 {
		t.Fatalf("valid OPTIONS failed: %d", got)
	}
	for i := 0; i < maxLoginFailures; i++ {
		if got := davSecurityRequest(h, "writer", "wrong", "PROPFIND").Code; got != 401 {
			t.Fatalf("failure challenge changed: %d", got)
		}
	}
	r := davSecurityRequest(h, "writer", "wrong-again", "PROPFIND")
	if r.Code != 401 || r.Header().Get("Retry-After") == "" || r.Header().Get("DAV") == "" {
		t.Fatal("blocked DAV capability/retry response missing")
	}
	if loginAllowed("dav:192.0.2.10") || !loginAllowed("192.0.2.10") {
		t.Fatal("DAV block affected browser or failed to activate")
	}
	if got := davSecurityRequest(h, "writer", authTestOldPassword, "PROPFIND").Code; got != 207 {
		t.Fatalf("cached mounted client blocked: %d", got)
	}
	securityLogin(t, h, "writer", authTestOldPassword)
}

func TestDAVStaleCacheAndRolesFailClosed(t *testing.T) {
	h := authSecurityFixture(t)
	if _, ok := davAuth("writer", authTestOldPassword); !ok {
		t.Fatal("initial DAV auth failed")
	}
	oldEntry, _ := davCache.Load("writer")
	admin := securityLogin(t, h, "admin", authTestOldPassword)
	if got := securityRequest(h, "POST", "/api/admin/user/password", admin, url.Values{"username": {"writer"}, "password": {authTestNewPassword}}).Code; got != 200 {
		t.Fatalf("reset status %d", got)
	}
	// Simulate a slow pre-reset verifier storing its result after invalidation.
	davCache.Store("writer", oldEntry)
	if _, ok := davAuth("writer", authTestOldPassword); ok {
		t.Fatal("stale cache accepted old password")
	}
	if _, ok := davAuth("writer", authTestNewPassword); !ok {
		t.Fatal("new DAV password rejected")
	}
	if _, err := db.Exec(`UPDATE users SET role='viewer' WHERE username='writer'`); err != nil {
		t.Fatal(err)
	}
	if got := davSecurityRequest(h, "writer", authTestNewPassword, "PUT").Code; got != 403 {
		t.Fatalf("cached old role authorized write: %d", got)
	}
	if got := securityRequest(h, "POST", "/protected/write", signSession("reader"), nil).Code; got != 403 {
		t.Fatalf("viewer wrote through API: %d", got)
	}
	if got := securityRequest(h, "POST", "/api/admin/user/password", signSession("reader"), url.Values{"username": {"writer"}, "password": {authTestOldPassword}}).Code; got != 403 {
		t.Fatalf("viewer changed someone else's password: %d", got)
	}
}

func TestLoginBlockUsesBlockedUntil(t *testing.T) {
	authSecurityFixture(t)
	loginAttempts["test"] = loginAttempt{First: time.Now().Add(-16 * time.Minute), Failures: 5, BlockedUntil: time.Now().Add(time.Minute)}
	if loginAllowed("test") {
		t.Fatal("first-window expiry bypassed an active block")
	}
	loginAttempts["test"] = loginAttempt{First: time.Now().Add(-16 * time.Minute), Failures: 5, BlockedUntil: time.Now().Add(-time.Second)}
	if !loginAllowed("test") {
		t.Fatal("expired block did not release")
	}
}

func testPlaybackTicket(t *testing.T, source, path string, expires int64) string {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS users(username TEXT PRIMARY KEY,role TEXT,session_version TEXT,folder_access TEXT);
		INSERT OR IGNORE INTO users(username,role,session_version) VALUES('test-player','viewer','')`); err != nil {
		t.Fatal(err)
	}
	return signStream(streamTicket{User: "test-player", Source: source, Path: path, Expires: expires})
}
