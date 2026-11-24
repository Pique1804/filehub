package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestArchiveExcludesPrivateStateAtEveryDepth(t *testing.T) {
	root := maintenanceFixture(t)
	for _, name := range []string{"keep.txt", ".ordinary-note", ".filehub-backups/index.db", "nested/.filehub-trash/1-1", "nested/.filehub-pending", "nested/keep.txt"} {
		full := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(full), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(full, []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Exec(`INSERT INTO shares(token,source,path,isdir,pwhash,expires,created) VALUES('archive','T','',1,'',0,1)`); e != nil {
		t.Fatal(e)
	}
	for _, public := range []bool{false, true} {
		rr := httptest.NewRecorder()
		if public {
			r := httptest.NewRequest("GET", "/s/archive", nil)
			r.SetPathValue("token", "archive")
			handleSharePublic(rr, r)
		} else {
			handleZip(rr, httptest.NewRequest("GET", "/api/zip?source=T&path=", nil))
		}
		if rr.Code != 200 {
			t.Fatal(rr.Code, rr.Body.String())
		}
		archive, e := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
		if e != nil {
			t.Fatal(e)
		}
		kept := 0
		for _, entry := range archive.File {
			if strings.Contains(entry.Name, ".filehub-") {
				t.Fatalf("private archive entry: %s", entry.Name)
			}
			if strings.HasSuffix(entry.Name, "keep.txt") || strings.HasSuffix(entry.Name, ".ordinary-note") {
				kept++
			}
		}
		if kept != 3 {
			t.Fatalf("ordinary files lost: %d", kept)
		}
	}
}

func TestActiveOriginalsAreAttachmentsAndSandboxed(t *testing.T) {
	root := maintenanceFixture(t)
	previous := authSecret
	authSecret = []byte("synthetic-play-secret")
	t.Cleanup(func() { authSecret = previous })
	if _, e := db.Exec(`INSERT INTO albums VALUES(1,'shared',1);
		INSERT INTO users(username,role) VALUES('publisher','admin');
		INSERT INTO album_shares(token,album_id,created,owner_id)
		SELECT 'public',1,1,account_id FROM users WHERE username='publisher'`); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"untrusted.html", "untrusted.svg"} {
		if e := os.WriteFile(filepath.Join(root, name), []byte("<script>fetch('/api/admin/user/create')</script>"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := db.Exec(`INSERT INTO album_photos VALUES(1,'T',?,1)`, name); e != nil {
			t.Fatal(e)
		}
		cases := []struct {
			r       *http.Request
			handler http.HandlerFunc
		}{
			{httptest.NewRequest("GET", "/api/download?source=T&path="+name+"&inline=1", nil), handleDownload},
			{httptest.NewRequest("GET", "/play/token", nil), handleStreamPlay},
			{httptest.NewRequest("GET", "/a/public/full?s=T&p="+name, nil), handleAlbumPublicFull},
		}
		cases[1].r.SetPathValue("token", testPlaybackTicket(t, "T", name, time.Now().Add(time.Hour).Unix()))
		cases[2].r.SetPathValue("token", "public")
		for _, c := range cases {
			rr := httptest.NewRecorder()
			c.handler(rr, c.r)
			if rr.Code != 200 {
				t.Fatalf("%s: %d %s", c.r.URL, rr.Code, rr.Body.String())
			}
			if !strings.HasPrefix(rr.Header().Get("Content-Disposition"), "attachment") {
				t.Fatal("active original inline", rr.Header())
			}
			if !strings.Contains(rr.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatal("missing document sandbox")
			}
		}
	}
}

func TestPassiveOriginalRangeStillWorks(t *testing.T) {
	root := testTrashSource(t)
	name := filepath.Join(root, "note.txt")
	os.WriteFile(name, []byte("0123456789"), 0600)
	r := httptest.NewRequest("GET", "/api/download?source=T&path=note.txt&inline=1", nil)
	r.Header.Set("Range", "bytes=2-5")
	rr := httptest.NewRecorder()
	handleDownload(rr, r)
	if rr.Code != 206 || rr.Body.String() != "2345" || !strings.HasPrefix(rr.Header().Get("Content-Disposition"), "inline") {
		t.Fatal(rr.Code, rr.Body.String(), rr.Header())
	}
}

func TestPrivateDirectoriesRejectPreplantedLinks(t *testing.T) {
	root := maintenanceFixture(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "sentinel"), []byte("keep"), 0600)
	file := filepath.Join(root, "photo.jpg")
	os.WriteFile(file, []byte("original"), 0600)
	for _, name := range []string{trashName, ".filehub-versions"} {
		if e := os.Symlink(outside, filepath.Join(root, name)); e != nil {
			t.Fatal(e)
		}
		if d, e := openInternalDir(root, name, true); e == nil {
			d.Close()
			t.Fatal("private directory link accepted")
		}
	}
	if e := moveToTrash(root, file, "photo.jpg"); e == nil {
		t.Fatal("trash followed link")
	}
	if _, e := savePhotoVersion("T", "photo.jpg", "test"); e == nil {
		t.Fatal("version followed link")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 1 {
		t.Fatal("external directory modified", entries)
	}
	if b, _ := os.ReadFile(file); string(b) != "original" {
		t.Fatal("source modified")
	}
}

func TestPinnedPrivateDirectorySurvivesNameReplacement(t *testing.T) {
	root := testTrashSource(t)
	outside := t.TempDir()
	d, e := openInternalDir(root, trashName, true)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	if e = os.Rename(filepath.Join(root, trashName), filepath.Join(root, "detached")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(root, trashName)); e != nil {
		t.Fatal(e)
	}
	if e = d.write("1-1.meta", []byte("pinned")); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "detached/1-1.meta")); string(b) != "pinned" {
		t.Fatal("write lost pinned directory")
	}
	if e = os.Symlink(filepath.Join(outside, "sentinel"), filepath.Join(root, "detached/leaf")); e != nil {
		t.Fatal(e)
	}
	if f, e := d.open("leaf", unix.O_WRONLY|unix.O_CREAT, 0600); e == nil {
		f.Close()
		t.Fatal("leaf link accepted")
	}
	if e = d.root.RemoveAll("leaf"); e != nil {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("external target modified")
	}
}

func TestTrustedProxyLoginIsolationAndSpoofing(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("X-Real-IP", "192.0.2.10")
	t.Setenv("FILEHUB_TRUSTED_PROXIES", "")
	if got := loginKey(r); got != "127.0.0.1" {
		t.Fatal("unconfigured proxy trusted", got)
	}
	t.Setenv("FILEHUB_TRUSTED_PROXIES", "127.0.0.1/32,::1/128")
	if got := loginKey(r); got != "192.0.2.10" {
		t.Fatal(got)
	}
	r.Header.Set("X-Real-IP", "192.0.2.11")
	if got := loginKey(r); got != "192.0.2.11" {
		t.Fatal(got)
	}
	for _, v := range []string{"192.0.2.1,192.0.2.2", "invalid", ""} {
		r.Header.Set("X-Real-IP", v)
		if got := loginKey(r); got != "127.0.0.1" {
			t.Fatal("malformed header accepted", got)
		}
	}
	r.Header.Set("X-Real-IP", "192.0.2.10")
	r.RemoteAddr = "198.51.100.5:4000"
	if got := loginKey(r); got != "198.51.100.5" {
		t.Fatal("direct client spoofed IP", got)
	}
}

func TestHTTPSModeRequiresTLSOrTheConfiguredProxy(t *testing.T) {
	t.Setenv("FILEHUB_REQUIRE_HTTPS", "1")
	t.Setenv("FILEHUB_TRUSTED_PROXIES", "127.0.0.1/32")
	h := securityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSession(w, r, "fixture", "fixture-version")
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		name, url, peer string
		forwarded       []string
		secure          bool
	}{
		{"direct TLS", "https://filehub.test/api/auth", "192.0.2.1:1234", nil, true},
		{"trusted proxy", "http://filehub.test/api/auth", "127.0.0.1:1234", []string{"https"}, true},
		{"multiple headers", "http://filehub.test/api/auth", "127.0.0.1:1234", []string{"http", "https"}, false},
		{"direct HTTP", "http://filehub.test/api/auth", "192.0.2.1:1234", []string{"https"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.url, nil)
			r.RemoteAddr = tc.peer
			for _, value := range tc.forwarded {
				r.Header.Add("X-Forwarded-Proto", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if tc.secure {
				if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("Set-Cookie"), "Secure") || w.Header().Get("Strict-Transport-Security") == "" {
					t.Fatalf("HTTPS rejected or unprotected: %d %v", w.Code, w.Header())
				}
			} else if w.Code != http.StatusUpgradeRequired || w.Header().Get("Set-Cookie") != "" {
				t.Fatalf("insecure request accepted: %d %v", w.Code, w.Header())
			}
		})
	}
}

func TestStoredVersionRejectsLinkBeforePhoneVerification(t *testing.T) {
	root := maintenanceFixture(t)
	outside := t.TempDir()
	os.Mkdir(filepath.Join(root, ".filehub-versions"), 0700)
	os.WriteFile(filepath.Join(outside, "private"), []byte("outside"), 0600)
	os.Symlink(filepath.Join(outside, "private"), filepath.Join(root, ".filehub-versions/forged"))
	if f, e := openStoredVersion("T", "forged"); e == nil {
		f.Close()
		t.Fatal("version link accepted")
	}
	// Legitimate immutable history is still readable and hashed by the pinned fd.
	os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("original bytes"), 0600)
	stored, e := savePhotoVersion("T", "photo.jpg", "test")
	if e != nil {
		t.Fatal(e)
	}
	f, e := openStoredVersion("T", stored)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	b, e := io.ReadAll(f)
	if e != nil || string(b) != "original bytes" {
		t.Fatal(string(b), e)
	}
}

func TestPrivateAliasesCannotBeDownloadedOrArchived(t *testing.T) {
	root := testTrashSource(t)
	os.Mkdir(filepath.Join(root, ".filehub-backups"), 0700)
	os.WriteFile(filepath.Join(root, ".filehub-backups/snapshot.db"), []byte("private sentinel"), 0600)
	os.Symlink(".filehub-backups", filepath.Join(root, "visible"))
	if _, ok := safePath("T", "visible/snapshot.db"); ok {
		t.Fatal("private alias accepted")
	}
	rr := httptest.NewRecorder()
	handleZip(rr, httptest.NewRequest("GET", "/api/zip?source=T&p=visible/snapshot.db", nil))
	archive, e := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range archive.File {
		f, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(f)
		f.Close()
		if bytes.Contains(b, []byte("private sentinel")) {
			t.Fatal("private alias exported")
		}
	}
	// An ordinary contained link keeps the existing download behavior.
	os.Mkdir(filepath.Join(root, "documents"), 0700)
	os.WriteFile(filepath.Join(root, "documents/note.txt"), []byte("ordinary"), 0600)
	os.Symlink("documents", filepath.Join(root, "notes"))
	if _, ok := safePath("T", "notes/note.txt"); !ok {
		t.Fatal("ordinary contained alias rejected")
	}
	alias := httptest.NewRecorder()
	handleZip(alias, httptest.NewRequest("GET", "/api/zip?source=T&p=notes/note.txt", nil))
	ordinary, e := zip.NewReader(bytes.NewReader(alias.Body.Bytes()), int64(alias.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	if len(ordinary.File) != 1 {
		t.Fatal("ordinary alias lost from archive")
	}
	file, e := ordinary.File[0].Open()
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	body, _ := io.ReadAll(file)
	if string(body) != "ordinary" {
		t.Fatal(string(body))
	}
}

func TestArchiveOpenRejectsLeafAndAncestorReplacement(t *testing.T) {
	root := testTrashSource(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "sentinel"), []byte("private"), 0600)
	for _, relative := range []string{"leaf", "parent/sentinel"} {
		if relative == "leaf" {
			os.Symlink(filepath.Join(outside, "sentinel"), filepath.Join(root, "leaf"))
		} else {
			os.Symlink(outside, filepath.Join(root, "parent"))
		}
		if f, e := openArchiveFile(root, relative); e == nil {
			f.Close()
			t.Fatal("archive followed replacement", relative)
		}
	}
	os.WriteFile(filepath.Join(root, "ordinary"), []byte("safe"), 0600)
	f, e := openArchiveFile(root, "ordinary")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "safe" {
		t.Fatal(string(b))
	}
}

func TestVerificationDoesNotBlockOnSpecialFile(t *testing.T) {
	root := testTrashSource(t)
	fifo := filepath.Join(root, "fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan bool, 1)
	go func() { matches, _ := verifyFileBytes(fifo, 0, ""); done <- matches }()
	select {
	case matches := <-done:
		if matches {
			t.Fatal("special file verified")
		}
	case <-time.After(time.Second):
		t.Fatal("verification blocked on FIFO")
	}
}
