package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"testing"
	"time"
)

func TestPhotoTextSearchDoesNotLosePhotoAfterDocumentMatches(t *testing.T) {
	oldDB, oldPhotos, oldAt := db, photoCacheList, photoCacheAt
	t.Cleanup(func() { db, photoCacheList, photoCacheAt = oldDB, oldPhotos, oldAt })
	var err error
	db, err = sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE VIRTUAL TABLE content USING fts5(body,source UNINDEXED,path UNINDEXED,name UNINDEXED,mtime UNINDEXED,tokenize='unicode61 remove_diacritics 2')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		if _, err := db.Exec(`INSERT INTO content(body,source,path,name,mtime) VALUES('договор','disk','doc','doc.pdf',0)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO content(body,source,path,name,mtime) VALUES('договор','disk','photo.jpg','photo.jpg',0)`); err != nil {
		t.Fatal(err)
	}
	photoCacheList = []Photo{{Name: "photo.jpg", Path: "photo.jpg", Source: "disk", ModTime: 123}}
	photoCacheAt = time.Now().Unix()
	rec := httptest.NewRecorder()
	handlePhotoTextSearch(rec, httptest.NewRequest("GET", "/api/search/photos/text?q=договор", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Photos []Photo `json:"photos"`
		Count  int     `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || len(got.Photos) != 1 || got.Photos[0].Path != "photo.jpg" {
		t.Fatalf("unexpected result: %+v", got)
	}

	rec = httptest.NewRecorder()
	handlePhotoTextSearch(rec, httptest.NewRequest("GET", "/api/search/photos/text?q=photo", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 0 {
		t.Fatalf("filename must not match OCR search: %+v", got)
	}

	if _, err := db.Exec(`INSERT INTO content(body,source,path,name,mtime) VALUES('счёт','disk','bill.jpg','bill.jpg',0)`); err != nil {
		t.Fatal(err)
	}
	photoCacheList = append(photoCacheList, Photo{Name: "bill.jpg", Path: "bill.jpg", Source: "disk", ModTime: 124})
	rec = httptest.NewRecorder()
	handlePhotoTextSearch(rec, httptest.NewRequest("GET", "/api/search/photos/text?q=счет", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || got.Photos[0].Path != "bill.jpg" {
		t.Fatalf("е/ё spelling missed: %+v", got)
	}
}

func TestPhotoTextSearchRespectsSourceAndFolder(t *testing.T) {
	oldDB, oldPhotos, oldAt := db, photoCacheList, photoCacheAt
	t.Cleanup(func() { db, photoCacheList, photoCacheAt = oldDB, oldPhotos, oldAt })
	var err error
	db, err = sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE VIRTUAL TABLE content USING fts5(body,source UNINDEXED,path UNINDEXED)`)
	if err != nil {
		t.Fatal(err)
	}
	photoCacheList = []Photo{{Source: "one", Path: "Photos/a.jpg"}, {Source: "one", Path: "Photos-other/b.jpg"}, {Source: "two", Path: "Photos/c.jpg"}}
	photoCacheAt = time.Now().Unix()
	for _, p := range photoCacheList {
		if _, err := db.Exec(`INSERT INTO content(body,source,path) VALUES('receipt',?,?)`, p.Source, p.Path); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	handlePhotoTextSearch(rec, httptest.NewRequest("GET", "/api/search/photos/text?q=receipt&source=one&path=Photos", nil))
	var got struct{ Photos []Photo }
	if err = json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Photos) != 1 || got.Photos[0].Path != "Photos/a.jpg" {
		t.Fatalf("escaped scope: %+v", got)
	}
}

func TestPhotoSearchTypeAndCaptureYear(t *testing.T) {
	oldDB, oldPhotos, oldAt := db, photoCacheList, photoCacheAt
	testDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db = testDB
	t.Cleanup(func() {
		testDB.Close()
		db, photoCacheList, photoCacheAt = oldDB, oldPhotos, oldAt
	})
	if _, err := db.Exec(`CREATE VIRTUAL TABLE content USING fts5(body,source UNINDEXED,path UNINDEXED);
		CREATE TABLE album_photos(album_id INTEGER, source TEXT, path TEXT);
		INSERT INTO content VALUES('2024 видео','disk','Family/screenshot.jpg'),
			('отпуск','disk','Family/photo.jpg'), ('отпуск','disk','Private/hidden.mp4');
		INSERT INTO album_photos VALUES(1,'disk','Family/photo.jpg'),(1,'disk','Family/отпуск.mp4'),
			(1,'disk','Private/hidden.mp4');`); err != nil {
		t.Fatal(err)
	}

	in2024 := time.Date(2024, 6, 5, 12, 0, 0, 0, time.UTC).Unix()
	in2026 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC).Unix()
	// These neighboring UTC timestamps straddle Jan 1 in the client's zone.
	end2024 := time.Date(2024, 12, 31, 20, 59, 59, 0, time.UTC).Unix()
	photoCacheList = []Photo{
		{Name: "screenshot.jpg", Source: "disk", Path: "Family/screenshot.jpg", ModTime: in2026},
		{Name: "new.mp4", Source: "disk", Path: "Family/new.mp4", ModTime: in2026, IsVideo: true},
		{Name: "boundary.jpg", Source: "disk", Path: "Family/boundary.jpg", ModTime: end2024 + 1},
		{Name: "отпуск.mp4", Source: "disk", Path: "Family/отпуск.mp4", ModTime: end2024, IsVideo: true},
		{Name: "another.mp4", Source: "disk", Path: "Family/another.mp4", ModTime: in2024, IsVideo: true},
		{Name: "photo.jpg", Source: "disk", Path: "Family/photo.jpg", ModTime: in2024},
		{Name: "hidden.mp4", Source: "disk", Path: "Private/hidden.mp4", ModTime: in2024, IsVideo: true},
	}
	sort.Slice(photoCacheList, func(i, j int) bool { return photoBefore(photoCacheList[i], photoCacheList[j]) })
	photoCacheAt = time.Now().Unix()
	profile := profileIdentity{folders: &folderAccess{Folders: []folderGrant{{Source: "disk", Path: "Family"}}}}
	search := func(q string, params url.Values) photoSearchResponse {
		t.Helper()
		params.Set("q", q)
		params.Set("mode", "auto")
		params.Set("zone", "Europe/Moscow")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/search/photos/text?"+params.Encode(), nil)
		handlePhotoTextSearch(rec, withProfile(req, profile))
		var got photoSearchResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("search %q status %d: %s", q, rec.Code, rec.Body.String())
		}
		return got
	}
	check := func(got photoSearchResponse, total int, names ...string) {
		t.Helper()
		var actual []string
		for _, item := range got.Photos {
			actual = append(actual, item.Name)
		}
		if got.Count != total || !slices.Equal(actual, names) {
			t.Fatalf("got %v (%d total), want %v (%d total)", actual, got.Count, names, total)
		}
	}

	check(search("2024", url.Values{}), 3, "отпуск.mp4", "another.mp4", "photo.jpg")
	check(search("видео", url.Values{}), 3, "new.mp4", "отпуск.mp4", "another.mp4")
	check(search("фото 2024 год", url.Values{}), 1, "photo.jpg")
	check(search("видео 2024 отпуск", url.Values{}), 1, "отпуск.mp4")
	check(search("2024 отпуск", url.Values{"album": {"1"}}), 2, "отпуск.mp4", "photo.jpg")
	check(search("2025", url.Values{}), 1, "boundary.jpg")
	check(search(`"2024"`, url.Values{}), 1, "screenshot.jpg")
	check(search("видео 2025", url.Values{}), 0)

	first := search("videos 2024", url.Values{"limit": {"1"}})
	check(first, 2, "отпуск.mp4")
	if first.NextCursor == "" || first.Intent == nil || first.Intent.Type != "video" || first.Intent.Year != 2024 {
		t.Fatalf("missing search cursor or intent: %+v", first)
	}
	second := search("videos 2024", url.Values{"limit": {"1"}, "after": {first.NextCursor}})
	check(second, 2, "another.mp4")
	if second.NextCursor != "" {
		t.Fatal("extra page after last matching video")
	}
}
