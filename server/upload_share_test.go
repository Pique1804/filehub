package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newUploadShareFixture(t *testing.T) *receiptFixture {
	t.Helper()
	f := newReceiptFixture(t)
	if err := initShares(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.root, "Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	registerShares(f.mux)
	f.mux.HandleFunc("/api/list", requireAuth(handleList))
	f.mux.HandleFunc("/api/download", requireAuth(handleDownload))
	return f
}

func (f *receiptFixture) makeReceiveLink(t *testing.T, conflict, count string) string {
	t.Helper()
	form := url.Values{"source": {"T"}, "path": {"Inbox"}, "days": {"7"}, "maxMiB": {"1"}, "maxFiles": {count}, "conflict": {conflict}}
	rr := f.call(http.MethodPost, "/api/share/create-upload", "writer", []byte(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var link struct{ URL string }
	if err := json.Unmarshal(rr.Body.Bytes(), &link); rr.Code != http.StatusOK || err != nil || !strings.HasPrefix(link.URL, "/u/") {
		t.Fatalf("create link: %d %s", rr.Code, rr.Body)
	}
	return link.URL
}

func (f *receiptFixture) receive(url, name, contents string) *httptest.ResponseRecorder {
	return f.call(http.MethodPost, url, "", []byte(contents), map[string]string{
		"Content-Type": "application/octet-stream", "X-Filehub-Filename": name,
	})
}

func (f *receiptFixture) listShare(t *testing.T, account, token string, expected bool) {
	t.Helper()
	rr := f.call(http.MethodGet, "/api/share/list", account, nil, nil)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), token) != expected {
		t.Fatalf("list for %s: %d %s", account, rr.Code, rr.Body)
	}
}

func (f *receiptFixture) revokeReceiveLink(t *testing.T, link, user string) {
	t.Helper()
	rr := f.call(http.MethodPost, "/api/share/delete?kind=upload&token="+strings.TrimPrefix(link, "/u/"), user, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body)
	}
}

func TestUploadLinkRevokedOnRestrictionAndAccountReuse(t *testing.T) {
	f := newUploadShareFixture(t)
	link := f.makeReceiveLink(t, "rename", "3")
	if _, err := db.Exec(`UPDATE users SET folder_access='{"folders":[]}' WHERE username='writer'`); err != nil {
		t.Fatal(err)
	}
	if result := f.receive(link, "fixture.txt", "sample"); result.Code != http.StatusGone {
		t.Fatalf("restricted upload link returned %d", result.Code)
	}
	if _, err := db.Exec(`UPDATE users SET folder_access=NULL WHERE username='writer'`); err != nil {
		t.Fatal(err)
	}
	if result := f.call(http.MethodGet, link, "", nil, nil); result.Code != http.StatusGone {
		t.Fatalf("removed restriction reactivated a revoked link: %d", result.Code)
	}
	newLink := f.makeReceiveLink(t, "rename", "3")
	if _, err := db.Exec(`DELETE FROM users WHERE username='writer'; INSERT INTO users(username,role) VALUES('writer','user')`); err != nil {
		t.Fatal(err)
	}
	if result := f.call(http.MethodGet, newLink, "", nil, nil); result.Code != http.StatusGone {
		t.Fatalf("recreated profile inherited a prior link: %d", result.Code)
	}
	f.listShare(t, "writer", strings.TrimPrefix(newLink, "/u/"), false)
}

func TestPublicLinkManagementChecksOwnerAndWriterRole(t *testing.T) {
	f := newUploadShareFixture(t)
	form := url.Values{"source": {"T"}, "path": {"Inbox"}}
	response := f.call(http.MethodPost, "/api/share/create", "writer", []byte(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var link struct{ Token string }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &link) != nil || link.Token == "" {
		t.Fatalf("create link: %d %s", response.Code, response.Body)
	}
	f.listShare(t, "writer", link.Token, true)
	f.listShare(t, "other", link.Token, false)
	f.listShare(t, "viewer", link.Token, false)
	revoke := "/api/share/delete?token=" + link.Token
	if response := f.call(http.MethodPost, revoke, "viewer", nil, nil); response.Code != http.StatusForbidden {
		t.Fatalf("viewer revoke: %d", response.Code)
	}
	f.call(http.MethodPost, revoke, "other", nil, nil)
	f.listShare(t, "writer", link.Token, true)
	if response := f.call(http.MethodPost, revoke, "writer", nil, nil); response.Code != http.StatusOK {
		t.Fatalf("owner revoke: %d", response.Code)
	}
	f.listShare(t, "writer", link.Token, false)
}

func TestPublicFileLinkFollowsOnlyThePublishedObject(t *testing.T) {
	f := newUploadShareFixture(t)
	file := filepath.Join(f.root, "original.txt")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"source": {"T"}, "path": {"original.txt"}}
	response := f.call(http.MethodPost, "/api/share/create", "writer", []byte(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var link struct{ URL string }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &link) != nil || link.URL == "" {
		t.Fatalf("create link: %d %s", response.Code, response.Body)
	}
	if result := f.call(http.MethodGet, link.URL, "", nil, nil); result.Code != http.StatusOK || result.Body.String() != "original" {
		t.Fatalf("download original: %d %s", result.Code, result.Body)
	}
	if err := os.Rename(file, filepath.Join(f.root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if result := f.call(http.MethodGet, link.URL, "", nil, nil); result.Code != http.StatusGone {
		t.Fatalf("replacement directory returned %d", result.Code)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := f.call(http.MethodGet, link.URL, "", nil, nil); result.Code != http.StatusGone {
		t.Fatalf("replacement file returned %d", result.Code)
	}
}

func TestUploadShareWriteOnlyAndRevoke(t *testing.T) {
	f := newUploadShareFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "Inbox", "old.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := f.makeReceiveLink(t, "rename", "3")
	token := strings.TrimPrefix(link, "/u/")
	f.listShare(t, "writer", token, true)
	f.listShare(t, "other", token, false)
	f.revokeReceiveLink(t, link, "other")
	f.listShare(t, "writer", token, true)

	page := f.call(http.MethodGet, link, "", nil, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `type="file"`) ||
		strings.Contains(page.Body.String(), "Inbox") || strings.Contains(page.Body.String(), "old.txt") {
		t.Fatalf("guest page: %d %s", page.Code, page.Body)
	}
	if page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("guest headers: %v", page.Header())
	}
	for request, want := range map[string]int{
		link + "/old.txt":                           http.StatusNotFound,
		"/s/" + token:                               http.StatusNotFound,
		"/api/list?source=T&path=Inbox":             http.StatusUnauthorized,
		"/api/download?source=T&path=Inbox/old.txt": http.StatusUnauthorized,
	} {
		if rr := f.call(http.MethodGet, request, "", nil, nil); rr.Code != want {
			t.Fatalf("unexpected guest read %s: %d", request, rr.Code)
		}
	}
	if rr := f.receive(link, "old.txt", "guest data"); rr.Code != http.StatusNoContent || rr.Body.Len() != 0 {
		t.Fatalf("guest submit: %d %s", rr.Code, rr.Body)
	}
	for name, want := range map[string]string{"old.txt": "existing", "old (1).txt": "guest data"} {
		body, err := os.ReadFile(filepath.Join(f.root, "Inbox", name))
		if err != nil || string(body) != want {
			t.Fatalf("saved %s: %q %v", name, body, err)
		}
	}
	f.revokeReceiveLink(t, link, "writer")
	f.listShare(t, "writer", token, false)
	if rr := f.receive(link, "later.txt", "after revoke"); rr.Code != http.StatusNotFound {
		t.Fatalf("revoked link accepted data: %d", rr.Code)
	}
}

func TestUploadShareEnforcesExpiryAndConflictPolicy(t *testing.T) {
	f := newUploadShareFixture(t)
	link := f.makeReceiveLink(t, "reject", "1")
	if err := os.WriteFile(filepath.Join(f.root, "Inbox", "existing.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []struct {
		name, contents string
		status         int
	}{
		{"existing.txt", "new", http.StatusConflict},
		{"outside-size.txt", strings.Repeat("x", uploadShareMiB+1), http.StatusRequestEntityTooLarge},
		{"unique.txt", "in scope", http.StatusNoContent},
		{"over-count.txt", "over file count", http.StatusGone},
	} {
		if rr := f.receive(link, attempt.name, attempt.contents); rr.Code != attempt.status {
			t.Fatalf("%s: %d %s", attempt.name, rr.Code, rr.Body)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(f.root, "Inbox")); err != nil || len(entries) != 2 {
		t.Fatalf("unexpected files: %v %v", entries, err)
	}
	expired := f.makeReceiveLink(t, "rename", "2")
	if _, err := db.Exec(`UPDATE upload_shares SET expires=1 WHERE token=?`, strings.TrimPrefix(expired, "/u/")); err != nil {
		t.Fatal(err)
	}
	if rr := f.receive(expired, "expired.txt", "never accepted"); rr.Code != http.StatusGone {
		t.Fatalf("expired link: %d", rr.Code)
	}
}

func TestUploadShareCountAndDestinationRemainBound(t *testing.T) {
	f := newUploadShareFixture(t)
	link := f.makeReceiveLink(t, "rename", "1")
	results := make(chan int, 2)
	for _, name := range []string{"one.txt", "two.txt"} {
		go func() { results <- f.receive(link, name, "synthetic bytes").Code }()
	}
	counts := map[int]int{}
	for range 2 {
		counts[<-results]++
	}
	if counts[http.StatusNoContent] != 1 || counts[http.StatusGone]+counts[http.StatusServiceUnavailable] != 1 {
		t.Fatalf("concurrent file count not enforced: %v", counts)
	}
	if rr := f.receive(link, "after.txt", "synthetic bytes"); rr.Code != http.StatusGone {
		t.Fatalf("a retry after full quota returned %d", rr.Code)
	}
	movedLink := f.makeReceiveLink(t, "rename", "2")
	path := filepath.Join(f.root, "Inbox")
	if err := os.Rename(path, filepath.Join(f.root, "Moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if rr := f.receive(movedLink, "inside.txt", "wrong folder"); rr.Code != http.StatusGone {
		t.Fatalf("replacement folder accepted bytes: %d", rr.Code)
	}
	if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
		t.Fatalf("replacement modified: %v %v", entries, err)
	}
}

func TestUploadShareLegacyShareStillDownloads(t *testing.T) {
	f := newUploadShareFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "sample.txt"), []byte("sample archive data"), 0o644); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"source": {"T"}, "path": {"sample.txt"}, "password": {"sample password"}, "days": {"2"}}
	rr := f.call(http.MethodPost, "/api/share/create", "writer", []byte(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var link struct{ URL string }
	if err := json.Unmarshal(rr.Body.Bytes(), &link); rr.Code != http.StatusOK || err != nil || !strings.HasPrefix(link.URL, "/s/") {
		t.Fatalf("legacy share creation: %d %s", rr.Code, rr.Body)
	}
	password := url.Values{"pw": {"sample password"}}
	rr = f.call(http.MethodPost, link.URL, "", []byte(password.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if rr.Code != http.StatusOK || rr.Body.String() != "sample archive data" || !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("legacy download: %d %s", rr.Code, rr.Body)
	}
}

type heldReceiveBody struct {
	io.Reader
	start, resume chan struct{}
	once          sync.Once
}

func (body *heldReceiveBody) Read(p []byte) (int, error) {
	body.once.Do(func() { close(body.start); <-body.resume })
	return body.Reader.Read(p)
}

func TestUploadShareRevocationDuringTransferAndTruncatedBody(t *testing.T) {
	f := newUploadShareFixture(t)
	link := f.makeReceiveLink(t, "rename", "2")
	request := func(body io.Reader) *http.Request {
		r := httptest.NewRequest(http.MethodPost, link, body)
		r.ContentLength = 4
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Filehub-Filename", "sample.txt")
		return r
	}
	truncated := httptest.NewRecorder()
	f.mux.ServeHTTP(truncated, request(strings.NewReader("xx")))
	if truncated.Code != http.StatusBadRequest {
		t.Fatalf("truncated data accepted: %d", truncated.Code)
	}
	body := &heldReceiveBody{Reader: strings.NewReader("data"), start: make(chan struct{}), resume: make(chan struct{})}
	complete := make(chan int, 1)
	go func() {
		rr := httptest.NewRecorder()
		f.mux.ServeHTTP(rr, request(body))
		complete <- rr.Code
	}()
	select {
	case <-body.start:
	case code := <-complete:
		t.Fatalf("upload failed before reading the body: %d", code)
	case <-time.After(5 * time.Second):
		close(body.resume)
		<-complete
		t.Fatal("upload did not begin reading the body")
	}
	busy := f.receive(link, "another.txt", "synthetic bytes").Code
	f.revokeReceiveLink(t, link, "writer")
	close(body.resume)
	if code := <-complete; code != http.StatusNotFound {
		t.Fatalf("in-flight revoked link accepted data: %d", code)
	}
	if busy != http.StatusServiceUnavailable {
		t.Fatalf("a second transfer used the same link while it was busy: %d", busy)
	}
	if entries, err := os.ReadDir(filepath.Join(f.root, "Inbox")); err != nil || len(entries) != 0 {
		t.Fatalf("partial files appeared in destination: %v %v", entries, err)
	}
}
