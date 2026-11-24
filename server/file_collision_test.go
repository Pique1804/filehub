package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameAndMoveNeverReplaceExistingFile(t *testing.T) {
	root := testTrashSource(t)
	for _, dir := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, data := range map[string]string{"a/from.txt": "original", "a/to.txt": "keep rename", "b/from.txt": "keep move"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		url string
		h   http.HandlerFunc
	}{
		{"/api/rename?source=T&path=a/from.txt&name=to.txt", handleRename},
		{"/api/move?source=T&path=a/from.txt&dest=b", handleMove},
		{"/api/copy?source=T&path=a/from.txt&dest=b", handleCopy},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		tc.h(rr, httptest.NewRequest(http.MethodPost, tc.url, nil))
		if rr.Code != http.StatusConflict {
			t.Fatalf("%s: status=%d, want 409", tc.url, rr.Code)
		}
	}
	for p, want := range map[string]string{"a/from.txt": "original", "a/to.txt": "keep rename", "b/from.txt": "keep move"} {
		got, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || string(got) != want {
			t.Fatalf("%s: contents=%q error=%v", p, got, err)
		}
	}
}

func TestUploadDoesNotOverwriteSameSizeDifferentBytes(t *testing.T) {
	root := testTrashSource(t)
	if err := os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("old!"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/upload?source=T&path=", strings.NewReader("new!"))
	req.Header.Set("X-Filename", "photo.jpg")
	rr := httptest.NewRecorder()
	handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload status=%d: %s", rr.Code, rr.Body.String())
	}
	for p, want := range map[string]string{"photo.jpg": "old!", "photo (1).jpg": "new!"} {
		got, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || string(got) != want {
			t.Fatalf("%s: contents=%q error=%v", p, got, err)
		}
	}
	req = httptest.NewRequest(http.MethodPost, "/api/upload?source=T&path=", strings.NewReader("new!"))
	req.Header.Set("X-Filename", "photo.jpg")
	rr = httptest.NewRecorder()
	handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("idempotent upload status=%d", rr.Code)
	}
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var photos int
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jpg") {
			photos++
		}
	}
	if photos != 2 {
		t.Fatalf("idempotent retry created %d photo files, want 2", photos)
	}
}
