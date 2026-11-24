package main

import (
	"bytes"
	"database/sql"
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

func textFixture(t *testing.T) (string, http.Handler) {
	t.Helper()
	root := testTrashSource(t)
	oldSecret := authSecret
	authSecret = []byte("unit-test-session-key-not-used-on-live-host")
	var err error
	db, err = sql.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE users(username TEXT PRIMARY KEY,role TEXT,session_version TEXT NOT NULL DEFAULT '',folder_access TEXT); INSERT INTO users(username,role) VALUES('writer','user'),('reader','viewer')"); err != nil {
		t.Fatal(err)
	}
	if err := initRevokedSessions(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		textMu.Lock()
		for _, r := range textRooms {
			if r.timer != nil {
				r.timer.Stop()
			}
		}
		textRooms = map[string]*textRoom{}
		textMu.Unlock()
		db.Close()
		authSecret = oldSecret
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/text", requireAuth(handleText))
	mux.HandleFunc("/api/text/edit", requireWriteMutation(handleTextEdit))
	mux.HandleFunc("/api/text/events", requireAuth(handleTextEvents))
	return root, mux
}
func textRequest(t *testing.T, h http.Handler, user, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if user != "" {
		r.AddCookie(&http.Cookie{Name: "fh_session", Value: signSession(user)})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func textDecode(t *testing.T, w *httptest.ResponseRecorder) textDocument {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var d textDocument
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestTextSingleWriterPersistenceAndConflict(t *testing.T) {
	root, h := textFixture(t)
	endpoint := "/api/text/edit?source=T&path=note.txt"
	textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "create"}))
	d := textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "claim"}))
	if d.Token == "" {
		t.Fatal("missing lease")
	}
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "claim"}); w.Code != 423 {
		t.Fatalf("second writer accepted: %d", w.Code)
	}
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "save", "token": "wrong"}); w.Code != 409 {
		t.Fatalf("wrong token accepted: %d", w.Code)
	}
	content := "Привет с ПК!\nВторая строка 📱 <script>not HTML</script>"
	d2 := textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "save", "token": d.Token, "revision": d.Revision, "content": content}))
	b, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(b) != content {
		t.Fatalf("persisted %q: %v", b, err)
	}
	if d2.Revision == d.Revision {
		t.Fatal("unchanged revision")
	}
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "save", "token": d.Token, "revision": d.Revision, "content": "stale"}); w.Code != 409 {
		t.Fatal("stale revision overwritten")
	}
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "create"}); w.Code != 409 {
		t.Fatal("create overwrote file")
	}
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "release", "token": d.Token}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	d3 := textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "claim"}))
	if d3.Token == d.Token {
		t.Fatal("lease token reused")
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("external edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "save", "token": d3.Token, "revision": d3.Revision, "content": "overwrite"}); w.Code != 409 {
		t.Fatal("external change overwritten")
	}
}
func TestTextPermissionsAndPaths(t *testing.T) {
	root, h := textFixture(t)
	endpoint := "/api/text/edit?source=T&path=note.txt"
	for _, u := range []string{"", "reader"} {
		w := textRequest(t, h, u, "POST", endpoint, map[string]string{"action": "create"})
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("write allowed for %q: %d", u, w.Code)
		}
	}
	if w := textRequest(t, h, "writer", "GET", endpoint, nil); w.Code != 405 {
		t.Fatal("GET mutation allowed")
	}
	r := httptest.NewRequest("POST", endpoint, strings.NewReader(`{"action":"create"}`))
	r.AddCookie(&http.Cookie{Name: "fh_session", Value: signSession("writer")})
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "secret.txt"), []byte("secret"), 0600)
	os.Symlink(out, filepath.Join(root, "escape"))
	os.Symlink(filepath.Join(out, "secret.txt"), filepath.Join(root, "link.txt"))
	for _, path := range []string{"escape/secret.txt", "link.txt", ".filehub-private/a.txt", "test.html", "test.go"} {
		if w := textRequest(t, h, "writer", "GET", "/api/text?source=T&path="+url.QueryEscape(path), nil); w.Code == 200 {
			t.Fatalf("invalid path read: %s", path)
		}
	}
	os.WriteFile(filepath.Join(root, "bad.txt"), []byte{0xff, 0xfe}, 0600)
	if w := textRequest(t, h, "writer", "GET", "/api/text?source=T&path=bad.txt", nil); w.Code == 200 {
		t.Fatal("invalid UTF8 accepted")
	}
	textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "create"}))
	textDecode(t, textRequest(t, h, "reader", "GET", "/api/text?source=T&path=note.txt", nil))
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "save", "content": strings.Repeat("x", maxTextBytes+1)}); w.Code != 413 {
		t.Fatal("oversized text accepted")
	}
}
func TestTextEventsCoalescingAndExpiredLease(t *testing.T) {
	root, h := textFixture(t)
	endpoint := "/api/text/edit?source=T&path=note.md"
	textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "create"}))
	d := textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "claim"}))
	key := filepath.Join(root, "note.md")
	textMu.Lock()
	room := textRooms[key]
	ch := make(chan []byte, 1)
	room.clients[ch] = true
	textMu.Unlock()
	textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "save", "token": d.Token, "revision": d.Revision, "content": "live text"}))
	textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "release", "token": d.Token})
	var event textDocument
	if err := json.Unmarshal(<-ch, &event); err != nil {
		t.Fatal(err)
	}
	if event.Content != "live text" || event.Editing {
		t.Fatalf("coalesced event lost data: %+v", event)
	}
	d = textDecode(t, textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "claim"}))
	textMu.Lock()
	room.expires = time.Now().Add(-time.Second)
	textMu.Unlock()
	if w := textRequest(t, h, "writer", "POST", endpoint, map[string]string{"action": "save", "token": d.Token, "revision": d.Revision, "content": "late"}); w.Code != 409 {
		t.Fatal("expired lease accepted")
	}
}
