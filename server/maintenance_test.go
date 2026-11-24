package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func maintenanceFixture(t *testing.T) string {
	t.Helper()
	root := testTrashSource(t)
	previous := db
	scope := photoScope
	photoScope = nil
	if e := initSearch(filepath.Join(t.TempDir(), "maintenance.db")); e != nil {
		t.Fatal(e)
	}
	current := db
	if _, e := db.Exec(`CREATE TABLE users(username TEXT PRIMARY KEY,role TEXT,session_version TEXT NOT NULL DEFAULT '',folder_access TEXT)`); e != nil {
		t.Fatal(e)
	}
	for _, f := range []func() error{initMaintenance, initPhotoMeta, initFavorites, initAlbums, initEmbed, initContent, initShares} {
		if e := f(); e != nil {
			t.Fatal(e)
		}
	}
	invalidateMediaCaches()
	t.Cleanup(func() { current.Close(); db = previous; photoScope = scope; invalidateMediaCaches() })
	return root
}

func TestTrashRemovesSearchRowsAndRollsBackOnIndexFailure(t *testing.T) {
	root := maintenanceFixture(t)
	const folder = "Фото_2026"
	const file = folder + "/note.txt"
	const sibling = "Фото_2026-другие.txt"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("count for %q = %d, want %d (error %v)", query, got, want, err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, folder), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, sibling} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{folder, sibling} {
		if err := indexAddPath("T", filepath.Join(root, path), path); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO content(rowid,body,source,path,name,mtime) VALUES(1,'fixture','T',?,'note.txt',1)`, file)
	exec(`INSERT INTO content_meta(source,path,size,mtime,method,chars,rowid_fts) VALUES('T',?,7,1,'plain',7,1)`, file)
	exec(`INSERT INTO favorites VALUES('T',?,1)`, file)
	exec(`CREATE TRIGGER reject_content_delete BEFORE DELETE ON content_meta BEGIN SELECT RAISE(ABORT,'fixture failure'); END`)

	if err := deleteFileToTrash("T", folder); err == nil {
		t.Fatal("expected the index error to reject deletion")
	}
	if _, err := os.Stat(filepath.Join(root, file)); err != nil {
		t.Fatal("original was not restored:", err)
	}
	count(`SELECT count(*) FROM files`, 3)
	count(`SELECT count(*) FROM content`, 1)
	count(`SELECT count(*) FROM favorites WHERE path='Фото_2026/note.txt'`, 1)

	exec(`DROP TRIGGER reject_content_delete`)
	if err := deleteFileToTrash("T", folder); err != nil {
		t.Fatal(err)
	}
	count(`SELECT count(*) FROM files`, 1)
	count(`SELECT count(*) FROM files WHERE path='Фото_2026-другие.txt'`, 1)
	count(`SELECT count(*) FROM content`, 0)
	count(`SELECT count(*) FROM content_meta`, 0)
	count(`SELECT count(*) FROM favorites WHERE path LIKE '.filehub-trash/%/note.txt'`, 1)
	if indexedN != 1 {
		t.Fatalf("indexed count = %d, want 1", indexedN)
	}
}

func TestAllPooledConnectionsWaitForWriter(t *testing.T) {
	maintenanceFixture(t)
	conns := []interface{ Close() error }{}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		c, e := db.Conn(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		conns = append(conns, c)
		var timeout int
		if e = c.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); e != nil || timeout != 5000 {
			t.Fatalf("conn %d timeout=%d err=%v", i, timeout, e)
		}
	}
}
func TestFavoriteDoesNotReportSuccessWhileWriteFailed(t *testing.T) {
	maintenanceFixture(t)
	writer, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	if _, e = writer.ExecContext(context.Background(), "BEGIN IMMEDIATE"); e != nil {
		t.Fatal(e)
	}
	defer writer.ExecContext(context.Background(), "ROLLBACK")
	rr := httptest.NewRecorder()
	handleFavToggle(rr, httptest.NewRequest("POST", "/api/fav/toggle?source=T&path=photo.jpg", nil))
	if rr.Code == 200 {
		t.Fatal("false success while database locked", rr.Body.String())
	}
}
func TestRenameRemapsEveryReferenceAndCache(t *testing.T) {
	root := maintenanceFixture(t)
	old := "before.jpg"
	next := "after.jpg"
	os.WriteFile(filepath.Join(root, old), []byte("photo"), 0600)
	indexAddPath("T", filepath.Join(root, old), old)
	for _, q := range []string{
		`INSERT INTO photo_meta(source,path,taken,lat,lon) VALUES('T','before.jpg',1577836800,55,37)`,
		`INSERT INTO favorites VALUES('T','before.jpg',1)`,
		`INSERT INTO albums VALUES(1,'test',1)`,
		`INSERT INTO album_photos VALUES(1,'T','before.jpg',1)`,
		`INSERT INTO photo_embed(source,path,model,dim,vec,ts) VALUES('T','before.jpg','ViT-B-32',1,x'0000803f',1)`,
		`INSERT INTO shares(token,source,path,isdir,pwhash,expires,created) VALUES('test','T','before.jpg',0,'',0,1)`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if len(cachedAllPhotos()) != 1 {
		t.Fatal("fixture")
	}
	rr := httptest.NewRecorder()
	handleRename(rr, httptest.NewRequest("POST", "/api/rename?source=T&path="+old+"&name="+next, nil))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for _, table := range []string{"files", "photo_meta", "favorites", "album_photos", "photo_embed", "shares"} {
		var oldN, newN int
		db.QueryRow("SELECT count(*) FROM "+table+" WHERE path=?", old).Scan(&oldN)
		db.QueryRow("SELECT count(*) FROM "+table+" WHERE path=?", next).Scan(&newN)
		if oldN != 0 || newN != 1 {
			t.Fatal(table, oldN, newN)
		}
	}
	if got := cachedAllPhotos(); len(got) != 1 || got[0].Path != next || got[0].ModTime != 1577836800 {
		t.Fatal(got)
	}
	if _, p := resolveAlias("T", old); p != next {
		t.Fatal(p)
	}
}
func TestDeleteInvalidatesPhotoCache(t *testing.T) {
	root := maintenanceFixture(t)
	os.WriteFile(filepath.Join(root, "a.jpg"), []byte("photo"), 0600)
	indexAddPath("T", filepath.Join(root, "a.jpg"), "a.jpg")
	if len(cachedAllPhotos()) != 1 {
		t.Fatal("fixture")
	}
	indexDelete("T", "a.jpg")
	if len(cachedAllPhotos()) != 0 {
		t.Fatal("stale cache")
	}
}
func TestOCRStatusAndWorkerHaveSameScopeAndRetryErrors(t *testing.T) {
	maintenanceFixture(t)
	photoScope = []scopeDir{{"T", "Photos"}}
	indexAdd("T", "Other/outside.jpg", "outside.jpg", false, 50000, 1)
	indexAdd("T", "Photos/failed.jpg", "failed.jpg", false, 40000, 123)
	_, e := db.Exec(`INSERT INTO content_meta(source,path,size,mtime,method,chars,status,done_at) VALUES('T','Photos/failed.jpg',40000,123,'ocr',0,3,1)`)
	if e != nil {
		t.Fatal(e)
	}
	stats := ocrLibraryStats()
	if stats["eligible"] != 1 || stats["errors"] != 1 || stats["pending"] != 0 {
		t.Fatal(stats)
	}
	pending := pendingFiles(true, 0)
	if len(pending) != 1 || pending[0].path != "Photos/failed.jpg" {
		t.Fatal(pending)
	}
	db.Exec(`UPDATE content_meta SET attempts=5`)
	if len(pendingFiles(true, 0)) != 0 {
		t.Fatal("unbounded retries")
	}
}
func TestTUSCompletionDurablyQueuesProcessing(t *testing.T) {
	f := newReceiptFixture(t)
	md := "filename " + base64.StdEncoding.EncodeToString([]byte("synthetic.jpg")) + ",source " + base64.StdEncoding.EncodeToString([]byte("T")) + ",path "
	created := f.call("POST", "/files/", "writer", nil, map[string]string{"Upload-Length": "4", "Upload-Metadata": md})
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	rr := f.patch(created.Header().Get("Location"), "0", []byte("test"))
	if rr.Code != 204 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM media_jobs WHERE source='T' AND path='synthetic.jpg'`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func TestSemanticFiltersOrphansBeforeLimit(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "visible.jpg")
	if err := os.WriteFile(full, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	indexAdd("T", "visible.jpg", "visible.jpg", false, 4, 1)
	for path, vec := range map[string][]float32{"deleted.jpg": {1, 0}, "visible.jpg": {0.8, 0.6}} {
		if _, e := db.Exec(`INSERT INTO photo_embed(source,path,model,dim,vec,ts) VALUES('T',?,'ViT-B-32',2,?,?)`, path, packVec(vec), time.Now().Unix()); e != nil {
			t.Fatal(e)
		}
	}
	result, _, err := rankVisualPhotos(httptest.NewRequest("GET", "/api/search/semantic", nil), []float32{1, 0}, 1, nil)
	if err != nil || len(result) != 1 || result[0].Path != "visible.jpg" {
		t.Fatalf("results: %#v, error: %v", result, err)
	}
}
func TestVersionsPreserveOriginalAndRestoreWithoutLosingEdit(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "photo.jpg")
	os.WriteFile(full, []byte("original EXIF and image"), 0600)
	indexAddPath("T", full, "photo.jpg")
	before := cachedAllPhotos()[0].Revision
	edit := filepath.Join(root, ".filehub-edit")
	os.WriteFile(edit, []byte("edited image"), 0600)
	if e := replacePhoto("T", "photo.jpg", edit, "crop"); e != nil {
		t.Fatal(e)
	}
	var id, stored string
	db.QueryRow(`SELECT id,stored FROM photo_versions`).Scan(&id, &stored)
	b, e := os.ReadFile(filepath.Join(root, ".filehub-versions", stored))
	if e != nil || string(b) != "original EXIF and image" {
		t.Fatal(string(b), e)
	}
	if cachedAllPhotos()[0].Revision == before {
		t.Fatal("revision not changed")
	}
	rr := httptest.NewRecorder()
	handleVersionRestore(rr, httptest.NewRequest("POST", "/api/photo/restore?id="+id, nil))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	b, _ = os.ReadFile(full)
	if string(b) != "original EXIF and image" {
		t.Fatal(string(b))
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM photo_versions`).Scan(&n)
	if n != 2 {
		t.Fatal("lost edit during restore", n)
	}
}
func TestDeltaOmitsUnchangedAndIncludesRemoval(t *testing.T) {
	root := maintenanceFixture(t)
	os.WriteFile(filepath.Join(root, "p.jpg"), []byte("photo"), 0600)
	indexAddPath("T", filepath.Join(root, "p.jpg"), "p.jpg")
	p := cachedAllPhotos()[0]
	known := map[string]string{"T|p.jpg": p.Revision + ":" + fmtInt(p.ModTime)}
	body, _ := json.Marshal(map[string]any{"known": known})
	rr := httptest.NewRecorder()
	handlePhotosDelta(rr, httptest.NewRequest("POST", "/api/photos/delta", strings.NewReader(string(body))))
	var out struct {
		Photos  []Photo  `json:"photos"`
		Removed []string `json:"removed"`
	}
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Photos) != 0 || len(out.Removed) != 0 {
		t.Fatal(rr.Body.String())
	}
	indexDelete("T", "p.jpg")
	rr = httptest.NewRecorder()
	handlePhotosDelta(rr, httptest.NewRequest("POST", "/api/photos/delta", strings.NewReader(string(body))))
	json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.Removed) != 1 {
		t.Fatal(rr.Body.String())
	}
}
func fmtInt(v int64) string { return strconv.FormatInt(v, 10) } // replaced below with strconv

func TestLargePhotoManifestAndSnapshotPages(t *testing.T) {
	maintenanceFixture(t)
	const count = 65536
	items := make([]Photo, count)
	for i := range items {
		name := "photo-" + strconv.Itoa(i) + ".jpg"
		items[i] = Photo{Name: name, Source: "T", Path: "Camera/" + name, Revision: "r1", ModTime: int64(count - i)}
	}
	setPhotos := func(photos []Photo) {
		photoCacheMu.Lock()
		defer photoCacheMu.Unlock()
		photoCacheList, photoCacheAt = photos, time.Now().Unix()
	}
	for _, size := range []int{50000, 50001, count} {
		known := make(map[string]string, size)
		for _, p := range items[:size] {
			known[p.Source+"|"+p.Path] = p.Revision + ":" + strconv.FormatInt(p.ModTime, 10)
		}
		updated := append([]Photo{{Name: "new.jpg", Source: "T", Path: "Camera/new.jpg", ModTime: count + 1}}, items[:size-1]...)
		setPhotos(updated)
		body, err := json.Marshal(map[string]any{"known": known})
		if err != nil || len(body) >= photoManifestBytes {
			t.Fatalf("invalid fixture manifest with %d items: %v", size, err)
		}
		legacy := httptest.NewRecorder()
		handlePhotosDelta(legacy, httptest.NewRequest(http.MethodPost, "/api/photos/delta", strings.NewReader(string(body))))
		var delta struct {
			Photos  []Photo
			Removed []string
			Token   string
		}
		if legacy.Code != http.StatusOK || json.Unmarshal(legacy.Body.Bytes(), &delta) != nil ||
			len(delta.Photos) != 1 || delta.Photos[0].Name != "new.jpg" || len(delta.Removed) != 1 || delta.Removed[0] != "T|"+items[size-1].Path || delta.Token != photoManifestToken(updated) {
			t.Fatalf("delta failed with %d items: %d %s", size, legacy.Code, legacy.Body.String())
		}
	}
	setPhotos(items)
	delta := httptest.NewRecorder()
	handlePhotosDelta(delta, httptest.NewRequest(http.MethodPost, "/api/photos/delta?paged=1", strings.NewReader(`{"known":{}}`)))
	if !strings.Contains(delta.Body.String(), `"snapshotRequired":true`) {
		t.Fatalf("large initial sync did not switch to pages: %s", delta.Body.String())
	}
	snapshot := photoManifestToken(items)
	readPage := func(after string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		handlePhotos(r, httptest.NewRequest(http.MethodGet, "/api/photos?snapshot="+snapshot+"&after="+url.QueryEscape(after), nil))
		return r
	}
	var received int
	var after string
	for {
		r := readPage(after)
		var page struct {
			Photos     []Photo
			Total      int
			NextCursor string
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &page) != nil || page.Total != count || len(page.Photos) == 0 || len(page.Photos) > photoSnapshotPageLimit {
			t.Fatalf("invalid snapshot page at %d: %d", received, r.Code)
		}
		for _, p := range page.Photos {
			if received >= count || p.Path != items[received].Path {
				t.Fatalf("unexpected snapshot row at %d: %v", received, p)
			}
			received++
		}
		if page.NextCursor == "" {
			break
		}
		after = page.NextCursor
	}
	if received != count {
		t.Fatalf("received %d of %d photos", received, count)
	}
	setPhotos(items[1:])
	if r := readPage(after); r.Code != http.StatusConflict {
		t.Fatalf("changed snapshot accepted an old continuation: %d", r.Code)
	}
}

func TestReconcileDetectsMissingChangedAndRetainedOriginal(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "p.jpg")
	original := []byte("original content")
	os.WriteFile(full, original, 0600)
	indexAddPath("T", full, "p.jpg")
	hash, _, err := currentHash("T", "p.jpg")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"source":"T","items":[{"key":"i:1","path":"p.jpg","size":16,"sha256":"` + hash + `"}]}`
	valid := func() bool {
		rr := httptest.NewRecorder()
		handleBackupReconcile(rr, httptest.NewRequest("POST", "/api/backup/reconcile", strings.NewReader(body)))
		var v struct {
			Items []struct {
				Valid bool `json:"valid"`
			} `json:"items"`
		}
		if e := json.Unmarshal(rr.Body.Bytes(), &v); e != nil || len(v.Items) != 1 {
			t.Fatal(rr.Body.String(), e)
		}
		return v.Items[0].Valid
	}
	if !valid() {
		t.Fatal("initial receipt rejected")
	}
	os.Rename(full, full+".missing")
	if valid() {
		t.Fatal("missing file confirmed")
	}
	os.Rename(full+".missing", full)
	edit := filepath.Join(root, ".edit")
	os.WriteFile(edit, []byte("edited"), 0600)
	if e := replacePhoto("T", "p.jpg", edit, "crop"); e != nil {
		t.Fatal(e)
	}
	if !valid() {
		t.Fatal("immutable original not recognized")
	}
	var stored string
	db.QueryRow(`SELECT stored FROM photo_versions LIMIT 1`).Scan(&stored)
	os.WriteFile(filepath.Join(root, ".filehub-versions", stored), []byte("corrupt original"), 0600)
	if valid() {
		t.Fatal("corrupt immutable original confirmed")
	}
}

func TestUnicodeDirectoryRemapPreservesSubtreeSuffix(t *testing.T) {
	root := maintenanceFixture(t)
	os.MkdirAll(filepath.Join(root, "Фото", "Ещё"), 0700)
	full := filepath.Join(root, "Фото", "Ещё", "тест.jpg")
	os.WriteFile(full, []byte("photo"), 0600)
	indexAddPath("T", filepath.Join(root, "Фото"), "Фото")
	if e := os.Rename(filepath.Join(root, "Фото"), filepath.Join(root, "Альбом")); e != nil {
		t.Fatal(e)
	}
	if e := remapFileReferences("T", "Фото", "T", "Альбом"); e != nil {
		t.Fatal(e)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM files WHERE path='Альбом/Ещё/тест.jpg'`).Scan(&n)
	if n != 1 {
		t.Fatal("unicode subtree lost", n)
	}
}

func TestMislabeledMP4IsVideoWithoutChangingOriginal(t *testing.T) {
	root := maintenanceFixture(t)
	full := filepath.Join(root, "movie.jpg")
	// ISO-BMFF ftyp with isom/mp41 video brands; Go's MIME detector reads this header.
	bytes := []byte{0, 0, 0, 32, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0, 'i', 's', 'o', 'm', 'i', 's', 'o', '2', 'a', 'v', 'c', '1', 'm', 'p', '4', '1'}
	if e := os.WriteFile(full, bytes, 0600); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(full)
	if e := indexAdd("T", "movie.jpg", "movie.jpg", false, st.Size(), st.ModTime().Unix()); e != nil {
		t.Fatal(e)
	}
	before := cachedAllPhotos()
	if len(before) != 1 || before[0].IsVideo {
		t.Fatal("fixture must start with extension-based classification")
	}
	if e := indexAddPath("T", full, "movie.jpg"); e != nil {
		t.Fatal(e)
	}
	photos := cachedAllPhotos()
	if len(photos) != 1 || !photos[0].IsVideo {
		t.Fatal("mislabeled video treated as image", photos)
	}
	if photos[0].Revision == before[0].Revision || photoManifestToken(photos) == photoManifestToken(before) {
		t.Fatal("classification change must invalidate Android gallery and thumbnail caches")
	}
	if !actualVideoFile(full) {
		t.Fatal("thumbnail decoder would use image path")
	}
	if mime := mediaContentType(full); mime != "video/mp4" {
		t.Fatal(mime)
	}
	hash, _ := hashFile(full)
	if e := indexMediaType("T", "movie.jpg", full); e != nil {
		t.Fatal(e)
	}
	after, _ := hashFile(full)
	if after != hash {
		t.Fatal("source bytes changed")
	}
	for _, stream := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/download?source=T&path=movie.jpg", nil)
		r.Header.Set("Range", "bytes=0-7")
		w := httptest.NewRecorder()
		if stream {
			r.SetPathValue("token", testPlaybackTicket(t, "T", "movie.jpg", 4102444800))
			handleStreamPlay(w, r)
		} else {
			handleDownload(w, r)
		}
		if w.Code != http.StatusPartialContent || w.Header().Get("Content-Type") != "video/mp4" || w.Body.Len() != 8 {
			t.Fatalf("video playback range: %d %v %d", w.Code, w.Header(), w.Body.Len())
		}
	}
}
