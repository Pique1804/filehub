package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tus/lockfile"
)

func TestPartialUploadRetentionAndCancel(t *testing.T) {
	fixture := newReceiptFixture(t)
	if _, err := db.Exec("INSERT INTO users(username,role) VALUES('admin','admin')"); err != nil {
		t.Fatal(err)
	}
	expired := fixture.create(t, "20")
	active := fixture.create(t, "20")
	fresh := fixture.create(t, "20")
	complete := fixture.create(t, "6")
	if out := fixture.patch(complete, "0", []byte("finish")); out.Code != http.StatusNoContent {
		t.Fatalf("complete upload %d: %s", out.Code, out.Body)
	}
	if out := fixture.patch(fresh, "0", []byte("part")); out.Code != http.StatusNoContent {
		t.Fatalf("partial upload %d: %s", out.Code, out.Body)
	}
	id := func(url string) string { return url[strings.LastIndexByte(url, '/')+1:] }
	for _, url := range []string{expired, active} {
		old := time.Now().Add(-partialUploadRetention - time.Hour)
		for _, suffix := range []string{"", ".info"} {
			if err := os.Chtimes(filepath.Join(fixture.stage, id(url)+suffix), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	lock := lockfile.Lockfile(filepath.Join(fixture.stage, id(active)+".lock"))
	if err := lock.TryLock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	sweepPartialUploads(fixture.stage)
	for _, suffix := range []string{"", ".info"} {
		if _, err := os.Stat(filepath.Join(fixture.stage, id(expired)+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale upload remains, error = %v", err)
		}
	}
	if got := fixture.call("GET", "/api/admin/uploads", "writer", nil, nil); got.Code != http.StatusForbidden {
		t.Fatalf("non-admin response %d", got.Code)
	}
	list := fixture.call("GET", "/api/admin/uploads", "admin", nil, nil)
	var response struct {
		Items         []partialUpload
		Count         int
		Bytes         int
		RetentionDays int
	}
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &response) != nil ||
		response.Count != 2 || response.RetentionDays != 7 || response.Bytes != 4 ||
		response.Items[0].ID != id(fresh) || response.Items[1].ID != id(active) {
		t.Fatalf("unexpected admin response %d: %s", list.Code, list.Body)
	}
	cancel := func(url string, want int) {
		t.Helper()
		got := fixture.call("POST", "/api/admin/uploads/cancel?id="+id(url), "admin", nil, nil)
		if got.Code != want {
			t.Fatalf("cancel status %d, want %d: %s", got.Code, want, got.Body)
		}
	}
	cancel(active, http.StatusConflict)
	cancel(fresh, http.StatusNoContent)
	cancel(complete, http.StatusNotFound)
	receipt := fixture.call("GET", receiptURL(complete), "writer", nil, nil)
	if contents, err := os.ReadFile(filepath.Join(fixture.root, "phone.mp4")); err != nil || string(contents) != "finish" ||
		receipt.Code != http.StatusOK {
		t.Fatalf("completed upload changed: %v, receipt status %d", err, receipt.Code)
	}
}
