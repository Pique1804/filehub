package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvitationLifecycle(t *testing.T) {
	mux := authSecurityFixture(t).(*http.ServeMux)
	db.SetMaxOpenConns(1)
	registerInvitations(mux)
	mux.HandleFunc("/api/auth", handleAuthStatus)
	admin := securityLogin(t, mux, "admin", authTestOldPassword)
	writer := securityLogin(t, mux, "writer", authTestOldPassword)
	for _, path := range []string{"Private", "Family"} {
		if err := os.Mkdir(filepath.Join(sources[0].Root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}

	makeInvite := func(cookie, role string, status int) string {
		t.Helper()
		payload := `{"role":"` + role + `","label":"Family invitation","folders":[{"source":"T","path":"Family"}],"personal":{"source":"T","path":"Private"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/invitations", strings.NewReader(payload))
		req.AddCookie(&http.Cookie{Name: "fh_session", Value: cookie})
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("create invitation: %d, want %d: %s", response.Code, status, response.Body)
		}
		if status != http.StatusOK {
			return ""
		}
		var item struct {
			Token   string
			Expires int64
		}
		if json.Unmarshal(response.Body.Bytes(), &item) != nil || item.Token == "" || time.Until(time.Unix(item.Expires, 0)) < 23*time.Hour {
			t.Fatalf("invalid invitation result: %s", response.Body)
		}
		return item.Token
	}
	redeem := func(token, name string) *httptest.ResponseRecorder {
		form := url.Values{"token": {token}, "username": {name}, "password": {"test pass+пароль 42"}}
		return securityRequest(mux, "POST", "/api/invite/accept", "", form)
	}
	reject := func(token, name string, status int) {
		t.Helper()
		if response := redeem(token, name); response.Code != status {
			t.Fatalf("accept invitation: %d, want %d: %s", response.Code, status, response.Body)
		}
	}
	revoke := func(token string) {
		t.Helper()
		digest, ok := inviteDigest(token)
		if !ok {
			t.Fatal("invalid token")
		}
		if response := securityRequest(mux, "POST", "/api/admin/invitations/revoke?id="+digest, admin, nil); response.Code != 204 {
			t.Fatalf("revoke invitation: %d %s", response.Code, response.Body)
		}
	}

	makeInvite(writer, "user", 403)
	makeInvite(admin, "admin", 400)
	token := makeInvite(admin, "user", 200)
	digest, _ := inviteDigest(token)
	if list := securityRequest(mux, "GET", "/api/admin/invitations", admin, nil); list.Code != 200 ||
		strings.Contains(list.Body.String(), token) || !strings.Contains(list.Body.String(), digest) {
		t.Fatal("invitations list did not preserve secret-token handling")
	}
	makeInvite(admin, "user", 409)
	reject(token, "admin", 409) // a taken username does not consume the invitation
	revoke(token)
	reject(token, "revoked", 410)
	token = makeInvite(admin, "user", 200)
	digest, _ = inviteDigest(token)
	if _, err := db.Exec("UPDATE profile_invitations SET expires=? WHERE digest=?", time.Now().Unix()-1, digest); err != nil {
		t.Fatal(err)
	}
	reject(token, "expired", 410)
	token = makeInvite(admin, "user", 200)

	// Concurrent requests cannot create two profiles from a single link.
	results := make(chan *httptest.ResponseRecorder, 2)
	go func() { results <- redeem(token, "invite-first") }()
	go func() { results <- redeem(token, "invite-second") }()
	first, second := <-results, <-results
	if second.Code == 204 {
		first, second = second, first
	}
	if first.Code != 204 || second.Code != 410 || len(first.Result().Cookies()) != 1 {
		t.Fatalf("concurrent acceptance returned %d and %d", first.Code, second.Code)
	}
	accepted := securityRequest(mux, "GET", "/api/auth", first.Result().Cookies()[0].Value, nil)
	var account struct {
		Authed     bool
		Username   string
		Role       string
		Restricted bool
	}
	if json.Unmarshal(accepted.Body.Bytes(), &account) != nil || !account.Authed || account.Role != "user" || !account.Restricted {
		t.Fatal("invitation did not start the scoped session")
	}
	profile, err := loadProfile(account.Username)
	if err != nil || profile.folders.Personal.Path != "Private" || !profile.folders.canRead("T", "Family/photo") ||
		profile.folders.canRead("T", "Outside/file") || profile.folders.canWrite("T", "Family", true) {
		t.Fatal("invited profile got incorrect folder grants", err)
	}
	securityLogin(t, mux, account.Username, "test pass+пароль 42")
	reject(token, "replay", 410)
}
