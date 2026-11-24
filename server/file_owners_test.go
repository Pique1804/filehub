package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileOwnersOnAuthenticatedSurfaces(t *testing.T) {
	root := maintenanceFixture(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = initShareOwners(tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(username,role,folder_access) VALUES
		('admin','admin',NULL), ('owner','user',NULL), ('personal','user','{"folders":[],"personal":{"source":"T","path":"private"}}')`)
	const shared = "shared/owner-demo.txt"
	const personal = "private/owner-personal.txt"
	const unknown = "shared/owner-unknown.txt"
	for _, file := range []string{shared, personal, unknown} {
		local := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte("sample file"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := indexAddPath("T", local, file); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO content(rowid,body,source,path,name,mtime) VALUES(1,'test document','T',?,'owner-demo.txt',1)`, shared)
	admin := profileIdentity{username: "admin", role: "admin"}
	reader := profileIdentity{username: "reader", role: "viewer", folders: &folderAccess{Folders: []folderGrant{{Source: "T", Path: "shared"}}}}

	call := func(handler http.HandlerFunc, profile profileIdentity, method, path string, payload any, status int) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		r := withProfile(httptest.NewRequest(method, path, bytes.NewReader(body)), profile)
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != status {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, w.Code, status, w.Body)
		}
		return w
	}
	getInfo := func(profile profileIdentity, file string) map[string]any {
		t.Helper()
		w := call(requireReadablePath(handleInfo), profile, "GET", "/api/info?source=T&path="+url.QueryEscape(file), nil, 200)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	update := func(profile profileIdentity, path, owner string, token any, status int) {
		t.Helper()
		call(requireAdminMutation(handleSetFileOwner), profile, "POST", "/api/admin/file-owner",
			map[string]any{"source": "T", "path": path, "owner": owner, "token": token}, status)
	}
	token := getInfo(admin, shared)["ownerEditToken"]
	if token == nil || getInfo(reader, shared)["ownerEditToken"] != nil || getInfo(reader, unknown)["owner"] != nil {
		t.Fatal("only admins should get an edit token and unassigned files should omit the owner")
	}
	if info := getInfo(admin, personal); info["owner"] != "personal" || info["ownerEditToken"] != nil {
		t.Fatalf("personal folder owner was not inherited: %#v", info)
	}
	call(requireReadablePath(handleInfo), reader, "GET", "/api/info?source=T&path="+url.QueryEscape(personal), nil, 404)
	update(reader, shared, "owner", token, 403)
	update(admin, shared, "owner", token, 204)

	for _, surface := range []struct {
		handler http.HandlerFunc
		path    string
	}{
		{handleList, "/api/list?source=T&path=shared"},
		{handleSearch, "/api/search?q=owner&offset=0"},
		{handleContentSearch, "/api/search/content?q=document"},
		{handleRecents, "/api/recents"},
	} {
		w := call(requireBrowse(surface.handler), reader, "GET", surface.path, nil, 200)
		if !strings.Contains(w.Body.String(), `"owner":"owner"`) || strings.Contains(w.Body.String(), "owner-personal") {
			t.Fatalf("wrong owner scope on %s: %s", surface.path, w.Body)
		}
	}
	key := ownerPath{"T", "shared/renamed.txt"}
	if err := moveFileReferences("T", shared, key.source, key.path); err != nil {
		t.Fatal(err)
	}
	if got := getInfo(reader, key.path)["owner"]; got != "owner" {
		t.Fatalf("owner after move = %v", got)
	}
	meta, err := deleteFileToTrashRecord(key.source, key.path)
	if err != nil {
		t.Fatal(err)
	}
	call(handleTrashRestore, admin, "POST", "/api/trash/restore?source=T&id="+url.QueryEscape(meta.ID), nil, 200)
	if got := getInfo(reader, key.path)["owner"]; got != "owner" {
		t.Fatalf("owner after restore = %v", got)
	}

	token = getInfo(admin, key.path)["ownerEditToken"]
	temp := filepath.Join(root, "shared", "replacement.txt")
	if err := os.WriteFile(temp, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, filepath.Join(root, key.path)); err != nil {
		t.Fatal(err)
	}
	if label := fileOwnerLabels(context.Background(), []ownerPath{key}, false)[key]; label != "" {
		t.Fatalf("replacement inherited owner %q", label)
	}
	update(admin, key.path, "owner", token, 409)
	token = getInfo(admin, key.path)["ownerEditToken"]
	update(admin, key.path, "owner", token, 204)
	exec(`DELETE FROM users WHERE username='owner'`)
	exec(`INSERT INTO users(username,role) VALUES('owner','user')`)
	if got := getInfo(reader, key.path)["owner"]; got != nil {
		t.Fatalf("recreated profile inherited owner %v", got)
	}
	update(admin, key.path, "", token, 204)
}
