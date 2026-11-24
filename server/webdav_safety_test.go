package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/webdav"
)

type brokenDAVBody struct{ sent bool }

func (b *brokenDAVBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, "partial"), nil
}
func (b *brokenDAVBody) Close() error { return nil }
func fixtureDAV() http.Handler {
	return davTransfers(&webdav.Handler{Prefix: "/dav", FileSystem: davFS{}, LockSystem: webdav.NewMemLS()})
}
func davRequest(h http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/dav/"+path, body)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func assertNoDAVStaging(t *testing.T, root string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, ".filehub-dav-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("staging leak", files, err)
	}
}

func TestDAVFinderInterruptedReplacementRestoresOnlyItsOwnPlaceholder(t *testing.T) {
	for _, scenario := range []string{"interrupted", "other-account", "recreated", "completed"} {
		t.Run(scenario, func(t *testing.T) {
			root := maintenanceFixture(t)
			const name = "document.txt"
			full := filepath.Join(root, name)
			const original = "original contents"
			if err := os.WriteFile(full, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO users(username,role) VALUES('first','admin'),('second','admin');
				INSERT INTO favorites(source,path,ts) VALUES('T','document.txt',1)`); err != nil {
				t.Fatal(err)
			}
			handler := davTransfers(&webdav.Handler{Prefix: "/dav", LockSystem: webdav.NewMemLS(),
				FileSystem: davFS{replacements: make(map[davReplacementPath]*davReplacement)}})
			request := func(method, user string, body io.Reader) int {
				r := httptest.NewRequest(method, "/dav/T/"+name, body)
				r.Header.Set("User-Agent", "WebDAVFS/3.0.0")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, withProfile(r, profileIdentity{username: user, role: "admin"}))
				return w.Code
			}
			if got := request("DELETE", "first", nil); got != http.StatusNoContent {
				t.Fatalf("delete status = %d", got)
			}
			if got := request("PUT", "first", strings.NewReader("")); got != http.StatusCreated {
				t.Fatalf("empty creation status = %d", got)
			}
			user, want := "first", original
			var body io.Reader = &brokenDAVBody{}
			switch scenario {
			case "other-account":
				user, want = "second", ""
			case "recreated":
				want = "other write"
				if err := os.WriteFile(full, []byte(want), 0600); err != nil {
					t.Fatal(err)
				}
			case "completed":
				want = "new contents"
				body = strings.NewReader(want)
			}
			if status := request("PUT", user, body); (status < 400) != (scenario == "completed") {
				t.Fatalf("replacement status = %d", status)
			}
			if data, err := os.ReadFile(full); err != nil || string(data) != want {
				t.Fatalf("contents = %q, error = %v, want %q", data, err, want)
			}
			if scenario == "interrupted" {
				var n int
				if err := db.QueryRow(`SELECT count(*) FROM favorites WHERE source='T' AND path='document.txt'`).Scan(&n); err != nil || n != 1 {
					t.Fatalf("restored original favorite = %d, error = %v", n, err)
				}
				if entries := readTrash(root); len(entries) != 0 {
					t.Fatalf("temporary empty file left in trash: %+v", entries)
				}
			} else if entries := readTrash(root); len(entries) != 1 {
				t.Fatalf("original trash entry missing: %+v", entries)
			}
			assertNoDAVStaging(t, root)
		})
	}
}

func TestDAVWindowsPropertyPatchDoesNotWriteOriginal(t *testing.T) {
	root := testTrashSource(t)
	const original = "existing file bytes"
	full := filepath.Join(root, "document.txt")
	if err := os.WriteFile(full, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	const patch = `<?xml version="1.0" encoding="utf-8"?>
<D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:"><D:set><D:prop>
<Z:Win32CreationTime>Sat, 21 Nov 2026 21:28:06 GMT</Z:Win32CreationTime>
<Z:Win32LastAccessTime>Sat, 21 Nov 2026 21:28:06 GMT</Z:Win32LastAccessTime>
<Z:Win32LastModifiedTime>Sat, 21 Nov 2026 21:28:06 GMT</Z:Win32LastModifiedTime>
<Z:Win32FileAttributes>00000020</Z:Win32FileAttributes>
</D:prop></D:set></D:propertyupdate>`
	out := davRequest(fixtureDAV(), "PROPPATCH", "T/document.txt", strings.NewReader(patch), nil)
	response := out.Body.String()
	if out.Code != http.StatusMultiStatus || !strings.Contains(response, "403 Forbidden") {
		t.Fatalf("property patch returned %d: %s", out.Code, response)
	}
	for _, name := range []string{"Win32CreationTime", "Win32LastAccessTime", "Win32LastModifiedTime", "Win32FileAttributes"} {
		if !strings.Contains(response, name) {
			t.Fatalf("missing result for %s: %s", name, response)
		}
	}
	ctx := context.WithValue(context.Background(), davTransferKey{}, &davTransfer{patch: true})
	if file, err := (davFS{}).OpenFile(ctx, "/T/document.txt", os.O_RDWR|os.O_TRUNC, 0o600); err == nil {
		file.Close()
		t.Fatal("patch context accepted a truncating write")
	}
	after, err := os.Stat(full)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("original changed during property patch: %v", err)
	}
	if body, err := os.ReadFile(full); err != nil || string(body) != original {
		t.Fatalf("original contents = %q, error = %v", body, err)
	}
	assertNoDAVStaging(t, root)
}

func TestDAVInterruptedPUTPreservesOriginal(t *testing.T) {
	for _, kind := range []string{"read-error", "short-length", "long-length", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root := maintenanceFixture(t)
			full := filepath.Join(root, "photo.jpg")
			os.WriteFile(full, []byte("original-photo-bytes"), 0600)
			var body io.Reader = strings.NewReader("partial")
			if kind == "read-error" {
				body = &brokenDAVBody{}
			}
			req := httptest.NewRequest("PUT", "/dav/T/photo.jpg", body)
			if kind == "short-length" {
				req.ContentLength = 100
			}
			if kind == "long-length" {
				req.ContentLength = 2
			}
			if kind == "cancelled" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			out := httptest.NewRecorder()
			fixtureDAV().ServeHTTP(out, req)
			if out.Code < 400 {
				t.Fatal("failed transfer accepted", out.Code)
			}
			b, e := os.ReadFile(full)
			if e != nil || string(b) != "original-photo-bytes" {
				t.Fatal("original lost", string(b), e)
			}
			var versions int
			db.QueryRow("SELECT count(*) FROM photo_versions").Scan(&versions)
			if versions != 0 {
				t.Fatal("incomplete transfer created version", versions)
			}
			assertNoDAVStaging(t, root)
		})
	}
}
func TestDAVInterruptedCreateNotPublished(t *testing.T) {
	root := maintenanceFixture(t)
	out := davRequest(fixtureDAV(), "PUT", "T/new.txt", &brokenDAVBody{}, nil)
	if out.Code < 400 {
		t.Fatal(out.Code)
	}
	if _, e := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(e) {
		t.Fatal("partial create published", e)
	}
	assertNoDAVStaging(t, root)
}
func TestDAVPUTVersionsAndFreshIndex(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "p.txt")
	original := []byte("original")
	os.WriteFile(full, original, 0600)
	indexAddPath("T", full, "p.txt")
	h := fixtureDAV()
	for _, body := range []string{"replacement", ""} {
		out := davRequest(h, "PUT", "T/p.txt", strings.NewReader(body), nil)
		if out.Code != 201 {
			t.Fatal(out.Code, out.Body.String())
		}
		got, _ := os.ReadFile(full)
		if string(got) != body {
			t.Fatal(string(got))
		}
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM files WHERE source='T' AND path='p.txt'`).Scan(&count)
	if count != 1 {
		t.Fatal("stale/duplicate index", count)
	}
	db.QueryRow(`SELECT count(*) FROM photo_versions WHERE source='T' AND path='p.txt'`).Scan(&count)
	if count != 2 {
		t.Fatal("missing originals", count)
	}
	oldHash := fmt.Sprintf("%x", sha256.Sum256(original))
	if valid, verified := reconcileIdentity("T", "p.txt", int64(len(original)), oldHash); !valid || !verified {
		t.Fatal("original receipt invalid")
	}
	assertNoDAVStaging(t, root)
}
func TestDAVDeleteRestorePreservesReferences(t *testing.T) {
	root := maintenanceFixture(t)
	os.WriteFile(filepath.Join(root, "p.jpg"), []byte("photo"), 0600)
	indexAddPath("T", filepath.Join(root, "p.jpg"), "p.jpg")
	db.Exec(`INSERT INTO favorites VALUES('T','p.jpg',1)`)
	out := davRequest(fixtureDAV(), "DELETE", "T/p.jpg", nil, nil)
	if out.Code != 204 {
		t.Fatal(out.Code, out.Body.String())
	}
	items := readTrash(root)
	if len(items) != 1 {
		t.Fatal("no recoverable trash", items)
	}
	if _, err := os.Stat(filepath.Join(root, "p.jpg")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleTrashRestore(w, httptest.NewRequest("POST", "/api/trash/restore?source=T&id="+items[0].ID, nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM favorites WHERE path='p.jpg'`).Scan(&n)
	if n != 1 {
		t.Fatal("favorite lost on restore")
	}
	b, _ := os.ReadFile(filepath.Join(root, "p.jpg"))
	if string(b) != "photo" {
		t.Fatal("restore bytes", string(b))
	}
}
func TestDAVDirectoryMoveRemapsReferences(t *testing.T) {
	root := maintenanceFixture(t)
	other := t.TempDir()
	byName["U"] = other
	os.MkdirAll(filepath.Join(root, "Фото", "Ещё"), 0700)
	old := "Фото/Ещё/p.jpg"
	next := "Album/Ещё/p.jpg"
	os.WriteFile(filepath.Join(root, old), []byte("photo"), 0600)
	indexAddPath("T", filepath.Join(root, "Фото"), "Фото")
	for _, q := range []string{
		`INSERT INTO favorites VALUES('T','Фото/Ещё/p.jpg',1)`,
		`INSERT INTO albums VALUES(1,'test',1)`,
		`INSERT INTO album_photos VALUES(1,'T','Фото/Ещё/p.jpg',1)`,
		`INSERT INTO shares(token,source,path,isdir,pwhash,expires,created) VALUES('test','T','Фото/Ещё/p.jpg',0,'',0,1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	out := davRequest(fixtureDAV(), "MOVE", "T/Фото", nil, map[string]string{"Destination": "/dav/U/Album", "Overwrite": "F"})
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body.String())
	}
	for _, table := range []string{"files", "favorites", "album_photos", "shares"} {
		var n int
		db.QueryRow("SELECT count(*) FROM "+table+" WHERE source='U' AND path=?", next).Scan(&n)
		if n != 1 {
			t.Fatal("unmapped", table, n)
		}
	}
	hash, _ := hashFile(filepath.Join(other, next))
	if valid, verified := reconcileIdentity("T", old, 5, hash); !valid || !verified {
		t.Fatal("old receipt lost")
	}
}
func TestDAVMoveMetadataFailureRollsBack(t *testing.T) {
	root := maintenanceFixture(t)
	os.WriteFile(filepath.Join(root, "before"), []byte("original"), 0600)
	indexAddPath("T", filepath.Join(root, "before"), "before")
	db.Exec(`CREATE TRIGGER fail_move BEFORE INSERT ON path_aliases BEGIN SELECT RAISE(ABORT,'fixture failure'); END`)
	out := davRequest(fixtureDAV(), "MOVE", "T/before", nil, map[string]string{"Destination": "/dav/T/after", "Overwrite": "F"})
	if out.Code < 400 {
		t.Fatal("database failure accepted")
	}
	b, e := os.ReadFile(filepath.Join(root, "before"))
	if e != nil || string(b) != "original" {
		t.Fatal("failed move lost source", e)
	}
	if _, e = os.Stat(filepath.Join(root, "after")); !os.IsNotExist(e) {
		t.Fatal("failed move published", e)
	}
}
func TestDAVCopyOverwriteRecoversOldDestination(t *testing.T) {
	root := maintenanceFixture(t)
	for p, b := range map[string]string{"from": "new", "to": "old"} {
		os.WriteFile(filepath.Join(root, p), []byte(b), 0600)
		indexAddPath("T", filepath.Join(root, p), p)
	}
	out := davRequest(fixtureDAV(), "COPY", "T/from", nil, map[string]string{"Destination": "/dav/T/to", "Overwrite": "T"})
	if out.Code != 204 {
		t.Fatal(out.Code, out.Body.String())
	}
	b, _ := os.ReadFile(filepath.Join(root, "to"))
	if string(b) != "new" {
		t.Fatal("copy bytes", string(b))
	}
	trash := readTrash(root)
	if len(trash) != 1 {
		t.Fatal("overwritten destination not retained")
	}
	b, _ = os.ReadFile(filepath.Join(trashDir(root), trash[0].ID))
	if string(b) != "old" {
		t.Fatal("old bytes lost")
	}
	assertNoDAVStaging(t, root)
}
func TestDAVConcurrentPUTKeepsEveryVersion(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "same")
	os.WriteFile(full, []byte("initial"), 0600)
	// Independent DAV instances exercise the shared API/DAV lock, not only DAV locks.
	var wg sync.WaitGroup
	codes := make(chan int, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes <- davRequest(fixtureDAV(), "PUT", "T/same", strings.NewReader(fmt.Sprintf("body-%d", i)), nil).Code
		}(i)
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 201 {
			t.Fatal(c)
		}
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM photo_versions WHERE path='same'`).Scan(&n)
	if n != 4 {
		t.Fatal("lost version", n)
	}
	assertNoDAVStaging(t, root)
}
