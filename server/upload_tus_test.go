package main

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestTUSFinalResponseReportsStoredPathWithoutClobber(t *testing.T) {
	root := testTrashSource(t)
	oldSecret := authSecret
	authSecret = []byte("filehub-test-session-secret")
	var err error
	err = initSearch(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE users(username TEXT PRIMARY KEY, role TEXT,session_version TEXT NOT NULL DEFAULT '',folder_access TEXT); INSERT INTO users(username,role) VALUES('writer','user')"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); authSecret = oldSecret })
	if err := os.WriteFile(filepath.Join(root, "video.mp4"), []byte("old!"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := registerTUS(mux, filepath.Join(t.TempDir(), "tus")); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "fh_session", Value: signSession("writer")})
		req.Header.Set("Tus-Resumable", "1.0.0")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	metadata := "filename " + base64.StdEncoding.EncodeToString([]byte("video.mp4")) + ",source " + base64.StdEncoding.EncodeToString([]byte("T")) + ",path " + base64.StdEncoding.EncodeToString([]byte(""))
	created := call(http.MethodPost, "/files/", nil, map[string]string{"Upload-Length": "4", "Upload-Metadata": metadata})
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d: %s", created.Code, created.Body.String())
	}
	location := created.Header().Get("Location")
	if location == "" {
		t.Fatal("missing TUS Location")
	}
	patched := call(http.MethodPatch, location, []byte("new!"), map[string]string{"Upload-Offset": "0", "Content-Type": "application/offset+octet-stream"})
	if patched.Code != http.StatusNoContent {
		t.Fatalf("patch=%d: %s", patched.Code, patched.Body.String())
	}
	if got := patched.Header().Get("X-Filehub-Path"); got != "video (1).mp4" {
		t.Fatalf("stored path=%q", got)
	}
	for path, want := range map[string]string{"video.mp4": "old!", "video (1).mp4": "new!"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != want {
			t.Fatalf("%s=%q, %v", path, got, err)
		}
	}
}
