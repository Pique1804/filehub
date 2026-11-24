package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testTrashSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	oldByName, oldSources, oldDB := byName, sources, db
	byName = map[string]string{"T": root}
	sources = []Source{{Name: "T", Root: root}}
	db = nil
	t.Cleanup(func() {
		byName, sources, db = oldByName, oldSources, oldDB
	})
	return root
}

func writeTestTrash(t *testing.T, root, filenameID, metaID, origPath string) {
	t.Helper()
	td := trashDir(root)
	if err := os.MkdirAll(td, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, filenameID), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(trashMeta{ID: metaID, Name: "payload", OrigPath: origPath, DeletedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, filenameID+".meta"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidTrashID(t *testing.T) {
	for _, id := range []string{"1-1", "123456789-42"} {
		if !validTrashID(id) {
			t.Fatalf("valid ID rejected: %q", id)
		}
	}
	for _, id := range []string{"", ".", "..", "1", "1-", "-1", "1-2-3", "../1-1", "1-a"} {
		if validTrashID(id) {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
}

func TestSafePathRejectsInternalComponents(t *testing.T) {
	root := testTrashSource(t)
	if _, ok := safePath("T", ".filehub-trash/1-1"); ok {
		t.Fatal("internal trash path accepted")
	}
	if _, ok := safePath("T", "docs/.filehub-private/item"); ok {
		t.Fatal("nested internal path accepted")
	}
	if got, ok := safePath("T", ""); !ok || got != root {
		t.Fatalf("source root should remain valid: got=%q ok=%v", got, ok)
	}
}

func TestTrashDeleteRejectsDotDot(t *testing.T) {
	root := testTrashSource(t)
	sentinel := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/trash/delete?source=T&id=..", nil)
	rr := httptest.NewRecorder()
	handleTrashDelete(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rr.Code)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("source root was modified: %v", err)
	}
}

func TestTrashRestoreRejectsSourceRoot(t *testing.T) {
	root := testTrashSource(t)
	writeTestTrash(t, root, "1-1", "1-1", "")
	req := httptest.NewRequest(http.MethodPost, "/api/trash/restore?source=T&id=1-1", nil)
	rr := httptest.NewRecorder()
	handleTrashRestore(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(trashDir(root), "1-1")); err != nil {
		t.Fatalf("trash payload moved on rejected restore: %v", err)
	}
}

func TestReadTrashRejectsForgedMetaID(t *testing.T) {
	root := testTrashSource(t)
	writeTestTrash(t, root, "1-1", "..", "docs/a.txt")
	if got := readTrash(root); len(got) != 0 {
		t.Fatalf("forged metadata accepted: %#v", got)
	}
}

func TestTrashRestoreRejectsSymlinkEscape(t *testing.T) {
	root := testTrashSource(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	writeTestTrash(t, root, "1-1", "1-1", "escape/out.txt")
	req := httptest.NewRequest(http.MethodPost, "/api/trash/restore?source=T&id=1-1", nil)
	rr := httptest.NewRecorder()
	handleTrashRestore(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(outside, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside destination was modified: %v", err)
	}
}

func TestTrashRestoreLegitimatePath(t *testing.T) {
	root := testTrashSource(t)
	writeTestTrash(t, root, "1-1", "1-1", "docs/a.txt")
	req := httptest.NewRequest(http.MethodPost, "/api/trash/restore?source=T&id=1-1", nil)
	rr := httptest.NewRecorder()
	handleTrashRestore(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", rr.Code, rr.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "a.txt"))
	if err != nil || string(b) != "payload" {
		t.Fatalf("legitimate restore failed: data=%q err=%v", b, err)
	}
}

func TestTrashRespectsPersonalFolder(t *testing.T) {
	mux := authSecurityFixture(t).(*http.ServeMux)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE files USING fts5(name, path UNINDEXED, source UNINDEXED, isdir UNINDEXED, size UNINDEXED, mtime UNINDEXED, tokenize='trigram')`); err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("/api/admin/user/folders", requireAdminMutation(setProfileFolders))
	mux.HandleFunc("/api/trash/list", requireBrowse(handleTrashList))
	mux.HandleFunc("/api/trash/restore", requireUploadMutation(handleTrashRestore))
	mux.HandleFunc("/api/trash/delete", requireUploadMutation(handleTrashDelete))
	mux.HandleFunc("/api/trash/empty", requireUploadMutation(handleTrashEmpty))
	admin := securityLogin(t, mux, "admin", authTestOldPassword)
	root := sources[0].Root
	personal := filepath.Join(root, "People", "writer")
	if err := os.MkdirAll(personal, 0700); err != nil {
		t.Fatal(err)
	}
	assign := httptest.NewRequest(http.MethodPost, "/api/admin/user/folders?username=writer",
		strings.NewReader(`{"restricted":true,"folders":[],"personal":{"source":"T","path":"People/writer"}}`))
	assign.AddCookie(&http.Cookie{Name: "fh_session", Value: admin})
	assignment := httptest.NewRecorder()
	mux.ServeHTTP(assignment, assign)
	if assignment.Code != http.StatusNoContent {
		t.Fatal(assignment.Code, assignment.Body.String())
	}
	writer := securityLogin(t, mux, "writer", authTestOldPassword)
	writeTestTrash(t, root, "1-1", "1-1", "People/writer/restore.txt")
	writeTestTrash(t, root, "1-2", "1-2", "People/writer/purge.txt")
	writeTestTrash(t, root, "1-3", "1-3", "People/other/keep.txt")
	writeTestTrash(t, root, "1-4", "1-4", "People/writer/missing/restore.txt")
	call := func(method, action, id, cookie string, status int) *httptest.ResponseRecorder {
		t.Helper()
		path := "/api/trash/" + action + "?source=T"
		if id != "" {
			path += "&id=" + id
		}
		w := securityRequest(mux, method, path, cookie, nil)
		if w.Code != status {
			t.Fatalf("%s: %d, want %d (%s)", path, w.Code, status, w.Body.String())
		}
		return w
	}
	call(http.MethodGet, "list", "", "", http.StatusUnauthorized)
	var listing struct {
		Items []trashMeta
	}
	w := call(http.MethodGet, "list", "", writer, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || len(listing.Items) != 3 || strings.Contains(w.Body.String(), "People/other") {
		t.Fatalf("scoped trash list: %s (%v)", w.Body.String(), err)
	}
	call(http.MethodPost, "restore", "1-3", writer, http.StatusNotFound)
	call(http.MethodPost, "delete", "1-3", writer, http.StatusNotFound)
	call(http.MethodPost, "restore", "1-4", writer, http.StatusConflict)
	call(http.MethodPost, "restore", "1-1", writer, http.StatusOK)
	if data, err := os.ReadFile(filepath.Join(personal, "restore.txt")); err != nil || string(data) != "payload" {
		t.Fatalf("personal restore: %q (%v)", data, err)
	}
	call(http.MethodPost, "empty", "", writer, http.StatusOK)
	if remaining := readTrash(root); len(remaining) != 1 || remaining[0].ID != "1-3" {
		t.Fatalf("personal empty affected other entries: %+v", remaining)
	}
	call(http.MethodPost, "empty", "", admin, http.StatusOK)
	if remaining := readTrash(root); len(remaining) != 0 {
		t.Fatalf("admin empty left entries: %+v", remaining)
	}
}
