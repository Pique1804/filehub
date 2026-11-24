package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileFolderIsolation(t *testing.T) {
	mux := authSecurityFixture(t).(*http.ServeMux)
	mux.HandleFunc("POST /api/admin/user/folders", requireAdminMutation(setProfileFolders))
	mux.HandleFunc("GET /api/list", requireBrowse(handleList))
	mux.HandleFunc("GET /api/download", requireReadablePath(handleDownload))
	mux.HandleFunc("GET /api/zip", requireBrowse(handleZip))
	mux.HandleFunc("GET /play/{token}", handleStreamPlay)
	admin := securityLogin(t, mux, "admin", authTestOldPassword)
	writer := securityLogin(t, mux, "writer", authTestOldPassword)
	other := securityLogin(t, mux, "other", authTestOldPassword)

	root := sources[0].Root
	for _, name := range []string{"Family", "Family private", "People/writer", "People/other"} {
		folder := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "test.txt"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "Family private"), filepath.Join(root, "Family", "link")); err != nil {
		t.Fatal(err)
	}

	change := func(cookie, username, policy string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/user/folders?username="+username, strings.NewReader(policy))
		req.AddCookie(&http.Cookie{Name: "fh_session", Value: cookie})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	const policy = `{"restricted":true,"folders":[{"source":"T","path":"Family"}],"personal":{"source":"T","path":"People/writer"}}`
	if response := change(writer, "writer", policy); response.Code != 403 {
		t.Fatalf("non-admin wrote grants: %d", response.Code)
	}
	if response := change(admin, "writer", policy); response.Code != 204 {
		t.Fatalf("assign: %d %s", response.Code, response.Body)
	}
	if response := change(admin, "other", policy); response.Code != 409 {
		t.Fatalf("duplicate personal root: %d", response.Code)
	}
	if response := change(admin, "other", `{"restricted":true,"folders":[],"personal":{"source":"T","path":"People/other"}}`); response.Code != 204 {
		t.Fatalf("second personal root: %d %s", response.Code, response.Body)
	}
	for _, c := range []struct {
		path, cookie string
		status       int
	}{
		{"/api/download?source=T&path=Family/test.txt", writer, 200},
		{"/api/download?source=T&path=Family%20private/test.txt", writer, 404},
		{"/api/download?source=T&path=Family/link/test.txt", writer, 404},
		{"/api/download?source=T&path=People/writer/test.txt", other, 404},
		{"/api/download?source=T&path=Family%20private/test.txt", admin, 200},
		{"/api/zip?source=T&path=", writer, 404},
		{"/api/zip?source=T&p=Family&p=People/other", writer, 404},
	} {
		if response := securityRequest(mux, "GET", c.path, c.cookie, nil); response.Code != c.status {
			t.Fatalf("%s: %d want %d", c.path, response.Code, c.status)
		}
	}
	response := securityRequest(mux, "GET", "/api/list?source=T&path=People", writer, nil)
	var list struct{ Entries []Entry }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list.Entries) != 1 || list.Entries[0].Name != "writer" {
		t.Fatalf("ancestor listing: %d %s", response.Code, response.Body)
	}
	dav := func(method, path, data string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/dav/T/"+path, strings.NewReader(data))
		req.SetBasicAuth("writer", authTestOldPassword)
		if method == "PROPFIND" {
			req.Header.Set("Depth", "1")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	if response := dav("PROPFIND", "", ""); response.Code != 207 || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("DAV listing: %d %s", response.Code, response.Body)
	}
	if response := dav("GET", "Family%20private/test.txt", ""); response.Code != 404 {
		t.Fatalf("DAV restricted read: %d", response.Code)
	}
	if response := dav("PUT", "Family/unchanged.txt", "denied"); response.Code != 403 {
		t.Fatalf("DAV wrote read-only folder: %d", response.Code)
	}
	if response := dav("MKCOL", "People/writer/new", ""); response.Code != 201 {
		t.Fatalf("DAV personal creation: %d %s", response.Code, response.Body)
	}

	account, err := loadProfile("writer")
	if err != nil {
		t.Fatal(err)
	}
	ticket := signStream(streamTicket{User: "writer", Version: account.version, Source: "T", Path: "Family/test.txt", Expires: time.Now().Add(time.Minute).Unix()})
	if response := securityRequest(mux, "GET", "/play/"+ticket, "", nil); response.Code != 200 {
		t.Fatal(response.Code)
	}
	if response := change(admin, "writer", `{"restricted":true,"folders":[]}`); response.Code != 204 {
		t.Fatal(response.Code)
	}
	if response := securityRequest(mux, "GET", "/play/"+ticket, "", nil); response.Code != 400 {
		t.Fatal(response.Code)
	}
	if response := dav("GET", "Family/test.txt", ""); response.Code != 404 {
		t.Fatal("cached DAV rights", response.Code)
	}
	if response := securityRequest(mux, "GET", "/api/list?source=T", writer, nil); response.Code != 404 {
		t.Fatal(response.Code)
	}
}
