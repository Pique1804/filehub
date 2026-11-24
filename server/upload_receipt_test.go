package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type receiptFixture struct {
	root, stage string
	mux         *http.ServeMux
}

func newReceiptFixture(t *testing.T) *receiptFixture {
	t.Helper()
	root := testTrashSource(t)
	previousSecret := authSecret
	authSecret = []byte("test-receipt-owner-secret")
	previousDB := db
	if err := initSearch(filepath.Join(t.TempDir(), "auth.db")); err != nil {
		t.Fatal(err)
	}
	if err := initMaintenance(); err != nil {
		t.Fatal(err)
	}
	testDB := db
	var err error
	_, err = db.Exec("CREATE TABLE users(username TEXT PRIMARY KEY,role TEXT,session_version TEXT NOT NULL DEFAULT '',folder_access TEXT); INSERT INTO users(username,role) VALUES('writer','user'),('other','user'),('viewer','viewer')")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testDB.Close(); db = previousDB; authSecret = previousSecret })
	f := &receiptFixture{root: root, stage: filepath.Join(t.TempDir(), "tus")}
	f.restart(t)
	return f
}
func (f *receiptFixture) restart(t *testing.T) {
	t.Helper()
	f.mux = http.NewServeMux()
	if err := registerTUS(f.mux, f.stage); err != nil {
		t.Fatal(err)
	}
}
func (f *receiptFixture) call(method, path, user string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "fh_session", Value: signSession(user)})
	req.Header.Set("Tus-Resumable", "1.0.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	f.mux.ServeHTTP(rr, req)
	return rr
}
func (f *receiptFixture) create(t *testing.T, size string) string {
	t.Helper()
	return f.createAt(t, size, "")
}
func (f *receiptFixture) createAt(t *testing.T, size, path string) string {
	t.Helper()
	md := "filename " + base64.StdEncoding.EncodeToString([]byte("phone.mp4")) + ",source " + base64.StdEncoding.EncodeToString([]byte("T")) + ",path " + base64.StdEncoding.EncodeToString([]byte(path))
	rr := f.call("POST", "/files/", "writer", nil, map[string]string{"Upload-Length": size, "Upload-Metadata": md})
	if rr.Code != 201 {
		t.Fatalf("create %d: %s", rr.Code, rr.Body.String())
	}
	return rr.Header().Get("Location")
}
func (f *receiptFixture) patch(url, offset string, b []byte) *httptest.ResponseRecorder {
	return f.call("PATCH", url, "writer", b, map[string]string{"Upload-Offset": offset, "Content-Type": "application/offset+octet-stream"})
}
func receiptURL(location string) string {
	return "/api/backup/status?id=" + location[strings.LastIndex(location, "/")+1:]
}

func TestReceiptSurvivesLostFinalResponseAndRestart(t *testing.T) {
	f := newReceiptFixture(t)
	loc := f.create(t, "10")
	if r := f.patch(loc, "0", []byte("0123")); r.Code != 204 {
		t.Fatalf("first chunk %d", r.Code)
	}
	if r := f.call("GET", receiptURL(loc), "writer", nil, nil); r.Code != 404 {
		t.Fatalf("partial confirmed: %d", r.Code)
	}
	f.restart(t)
	if r := f.patch(loc, "4", []byte("456789")); r.Code != 204 {
		t.Fatalf("resume %d: %s", r.Code, r.Body.String())
	}
	// Deliberately discard the final response, including X-Filehub-Path.
	f.restart(t)
	rr := f.call("GET", receiptURL(loc), "writer", nil, nil)
	if rr.Code != 200 {
		t.Fatalf("recover %d: %s", rr.Code, rr.Body.String())
	}
	var r uploadReceipt
	if err := json.Unmarshal(rr.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("0123456789"))
	if r.Path != "phone.mp4" || r.Size != 10 || r.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("bad receipt: %+v", r)
	}
	data, err := os.ReadFile(filepath.Join(f.root, r.Path))
	if err != nil || string(data) != "0123456789" {
		t.Fatalf("bad saved bytes %q %v", data, err)
	}
	if entries, _ := os.ReadDir(f.root); len(entries) != 1 {
		t.Fatalf("duplicate saved files: %d", len(entries))
	}
}
func TestReceiptRejectsOtherAccountAndChangedCopy(t *testing.T) {
	f := newReceiptFixture(t)
	loc := f.create(t, "4")
	if r := f.patch(loc, "0", []byte("good")); r.Code != 204 {
		t.Fatal(r.Code)
	}
	if r := f.call("GET", receiptURL(loc), "other", nil, nil); r.Code != 404 {
		t.Fatalf("other account got receipt: %d", r.Code)
	}
	path := filepath.Join(f.root, "phone.mp4")
	if err := os.WriteFile(path, []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	os.Chtimes(path, now, now)
	if r := f.call("GET", receiptURL(loc), "writer", nil, nil); r.Code != 410 {
		t.Fatalf("changed copy confirmed: %d", r.Code)
	}
}
func TestReceiptRetryDoesNotDuplicateOrReplace(t *testing.T) {
	f := newReceiptFixture(t)
	for _, content := range []string{"old!", "new!", "new!"} {
		loc := f.create(t, "4")
		if r := f.patch(loc, "0", []byte(content)); r.Code != 204 {
			t.Fatalf("patch %d: %s", r.Code, r.Body.String())
		}
		if r := f.call("GET", receiptURL(loc), "writer", nil, nil); r.Code != 200 {
			t.Fatal(r.Code)
		}
	}
	entries, _ := os.ReadDir(f.root)
	if len(entries) != 2 {
		t.Fatalf("expected 2 distinct files, got %d", len(entries))
	}
	for name, want := range map[string]string{"phone.mp4": "old!", "phone (1).mp4": "new!"} {
		b, e := os.ReadFile(filepath.Join(f.root, name))
		if e != nil || string(b) != want {
			t.Fatalf("%s: %q %v", name, b, e)
		}
	}
}

func TestConcurrentUploadsKeepDistinctBytesAndReceipts(t *testing.T) {
	f := newReceiptFixture(t)
	type result struct {
		location string
		body     []byte
		status   int
	}
	results := make(chan result, 3)
	for i := 0; i < 3; i++ {
		loc := f.create(t, "1048576")
		body := bytes.Repeat([]byte{byte('A' + i)}, 1048576)
		go func(location string, data []byte) {
			r := f.patch(location, "0", data)
			results <- result{location, data, r.Code}
		}(loc, body)
	}
	paths := map[string]bool{}
	for i := 0; i < 3; i++ {
		got := <-results
		if got.status != 204 {
			t.Fatalf("parallel upload: %d", got.status)
		}
		response := f.call("GET", receiptURL(got.location), "writer", nil, nil)
		var r uploadReceipt
		if response.Code != 200 {
			t.Fatal(response.Code)
		}
		if err := json.Unmarshal(response.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(f.root, r.Path))
		if err != nil || !bytes.Equal(data, got.body) {
			t.Fatalf("parallel content mismatch: %v", err)
		}
		digest := sha256.Sum256(data)
		if r.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal("parallel receipt checksum mismatch")
		}
		paths[r.Path] = true
	}
	if len(paths) != 3 {
		t.Fatalf("parallel files were overwritten: %d", len(paths))
	}
}
