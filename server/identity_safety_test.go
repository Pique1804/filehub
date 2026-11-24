package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcilePathReuseKeepsAllGenerations(t *testing.T) {
	root := maintenanceFixture(t)
	type receipt struct {
		size int64
		hash string
	}
	receipts := []receipt{}
	for _, dst := range []string{"one.txt", "two.txt", "three.txt"} {
		full := filepath.Join(root, "same.txt")
		body := []byte("contents for " + dst)
		if e := os.WriteFile(full, body, 0600); e != nil {
			t.Fatal(e)
		}
		indexAddPath("T", full, "same.txt")
		hash, _ := hashFile(full)
		receipts = append(receipts, receipt{int64(len(body)), hash})
		for _, receipt := range receipts {
			valid, verified := reconcileIdentity("T", "same.txt", receipt.size, receipt.hash)
			if !valid || !verified {
				t.Fatal("path reuse confused exact byte identity", dst, receipt)
			}
		}
		w := httptest.NewRecorder()
		handleRename(w, httptest.NewRequest("POST", "/api/rename?source=T&path=same.txt&name="+dst, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Move and edit an older generation; its exact receipt must find the version.
	if e := moveFileReferences("T", "one.txt", "T", "old-edited.txt"); e != nil {
		t.Fatal(e)
	}
	edit := filepath.Join(root, ".edit")
	os.WriteFile(edit, []byte("EDIT"), 0600)
	if e := replacePhoto("T", "old-edited.txt", edit, "fixture"); e != nil {
		t.Fatal(e)
	}
	for _, receipt := range receipts {
		valid, verified := reconcileIdentity("T", "same.txt", receipt.size, receipt.hash)
		if !valid || !verified {
			t.Fatal("historical receipt lost", receipt)
		}
	}
}
func TestReconcileDoesNotTrustCachedHashOrTrash(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "photo.txt")
	os.WriteFile(full, []byte("before"), 0600)
	st, _ := os.Stat(full)
	hash, _, _ := currentHash("T", "photo.txt")
	os.WriteFile(full, []byte("AFTER!"), 0600)
	os.Chtimes(full, st.ModTime(), st.ModTime())
	if valid, verified := reconcileIdentity("T", "photo.txt", 6, hash); valid || !verified {
		t.Fatal("cached hash authorized wrong bytes", valid, verified)
	}
	hash, _ = hashFile(full)
	if e := deleteFileToTrash("T", "photo.txt"); e != nil {
		t.Fatal(e)
	}
	if valid, verified := reconcileIdentity("T", "photo.txt", 6, hash); valid || !verified {
		t.Fatal("trash must not authorize phone cleanup", valid, verified)
	}
}
func TestReconcileReturnsPendingOnStorageReadFailure(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "photo.txt")
	os.WriteFile(full, []byte("before"), 0600)
	hash, _ := hashFile(full)
	// A loop makes filesystem access fail, distinct from a missing copy.
	os.Rename(full, full+".saved")
	os.Symlink("loop", filepath.Join(root, "loop"))
	os.Symlink("loop", full)
	body := `{"source":"T","items":[{"key":"a","path":"photo.txt","size":6,"sha256":"` + hash + `"}]}`
	w := httptest.NewRecorder()
	handleBackupReconcile(w, httptest.NewRequest("POST", "/api/backup/reconcile", strings.NewReader(body)))
	var out struct {
		Items []struct{ Valid, Verified bool }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	// Unsafe paths cannot be inspected, so fail closed without claiming a full check.
	if len(out.Items) != 1 || out.Items[0].Valid || out.Items[0].Verified {
		t.Fatal(w.Body.String())
	}
}
