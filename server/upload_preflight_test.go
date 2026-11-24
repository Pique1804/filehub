package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const preflightURL = "/api/upload/preflight"

func (f *receiptFixture) preflight(t *testing.T, user, folder, payload string, includeHash bool) *httptest.ResponseRecorder {
	t.Helper()
	size := int64(len(payload))
	req := uploadPreflight{Source: "T", Path: folder, Filename: "phone.mp4", Size: &size}
	if includeHash {
		hash := sha256.Sum256([]byte(payload))
		req.SHA256 = hex.EncodeToString(hash[:])
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return f.call(http.MethodPost, preflightURL, user, body, nil)
}

func TestUploadPreflightUsesFreshBytesAndSameDestination(t *testing.T) {
	f := newReceiptFixture(t)
	if err := initIntegrity(); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{"old!", "new!"} {
		loc := f.create(t, "4")
		if r := f.patch(loc, "0", []byte(payload)); r.Code != http.StatusNoContent {
			t.Fatalf("normal upload returned %d: %s", r.Code, r.Body)
		}
	}
	probe := f.preflight(t, "writer", "", "new!", false)
	if probe.Code != http.StatusOK || probe.Body.String() != "{\"candidate\":true}\n" {
		t.Fatalf("metadata probe: %d %s", probe.Code, probe.Body)
	}
	match := f.preflight(t, "writer", "", "new!", true)
	var receipt uploadReceipt
	if err := json.Unmarshal(match.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("verify: %d %s", match.Code, match.Body)
	}
	expected := sha256.Sum256([]byte("new!"))
	if match.Code != http.StatusOK || receipt.Source != "T" || receipt.Path != "phone (1).mp4" || receipt.Size != 4 || receipt.SHA256 != hex.EncodeToString(expected[:]) {
		t.Fatalf("incorrect receipt: %d %+v", match.Code, receipt)
	}
	if r := f.preflight(t, "writer", "archive", "new!", true); r.Code != http.StatusNoContent {
		t.Fatalf("another folder suppressed explicitly requested copy: %d %s", r.Code, r.Body)
	}
	loc := f.createAt(t, "4", "archive")
	if r := f.patch(loc, "0", []byte("new!")); r.Code != http.StatusNoContent {
		t.Fatalf("upload into archive: %d %s", r.Code, r.Body)
	}
	path := filepath.Join(f.root, receipt.Path)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if hash, _, err := currentHash("T", receipt.Path); err != nil || hash != receipt.SHA256 {
		t.Fatalf("test did not retain the previous cached hash: %q %v", hash, err)
	}
	if r := f.preflight(t, "writer", "", "new!", true); r.Code != http.StatusNoContent {
		t.Fatalf("same size and mtime authorized changed bytes: %d %s", r.Code, r.Body)
	}
	if r := f.preflight(t, "writer", "archive", "new!", true); r.Code != http.StatusOK {
		t.Fatalf("copy in its requested folder was not found: %d %s", r.Code, r.Body)
	}
}

func TestUploadPreflightRequiresWriteAccess(t *testing.T) {
	f := newReceiptFixture(t)
	for user, want := range map[string]int{"": http.StatusUnauthorized, "viewer": http.StatusForbidden} {
		if r := f.preflight(t, user, "", "data", true); r.Code != want {
			t.Fatalf("account %q got %d, want %d", user, r.Code, want)
		}
	}
	if r := f.preflight(t, "writer", ".filehub-trash", "data", true); r.Code != http.StatusBadRequest {
		t.Fatalf("internal folder accepted: %d", r.Code)
	}
}

func TestUploadPreflightWithConcurrentRepeat(t *testing.T) {
	f := newReceiptFixture(t)
	const count = 3
	completed := make(chan *httptest.ResponseRecorder, count)
	for range count {
		loc := f.create(t, "4")
		go func() { completed <- f.patch(loc, "0", []byte("same")) }()
	}
	for range count {
		if r := <-completed; r.Code != http.StatusNoContent || r.Header().Get("X-Filehub-Path") != "phone.mp4" {
			t.Fatalf("concurrent finalization: %d %s", r.Code, r.Body)
		}
	}
	size := int64(4)
	hash := sha256.Sum256([]byte("same"))
	body, err := json.Marshal(uploadPreflight{
		Source: "T", Filename: "phone.mp4", Size: &size, SHA256: hex.EncodeToString(hash[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	for range count {
		go func() { completed <- f.call(http.MethodPost, preflightURL, "writer", body, nil) }()
	}
	for range count {
		r := <-completed
		var receipt uploadReceipt
		if err := json.Unmarshal(r.Body.Bytes(), &receipt); err != nil || r.Code != http.StatusOK || receipt.Path != "phone.mp4" {
			t.Fatalf("concurrent preflight: %d %s", r.Code, r.Body)
		}
	}
	if entries, err := os.ReadDir(f.root); err != nil || len(entries) != 1 {
		t.Fatalf("repeat created additional files: %v %v", entries, err)
	}
}
