package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestIndexAddReplacesOnlyTheExactPath(t *testing.T) {
	oldDB, oldIndexed := db, indexedN
	if err := initSearch(filepath.Join(t.TempDir(), "index.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		db, indexedN = oldDB, oldIndexed
	})
	for _, name := range []string{`Фото "Лето"_100%.jpg`, "я"} {
		target := "Camera/" + name
		for _, entry := range []struct{ source, path, name string }{
			{"HDD1", target, name},
			{"HDD2", target, name},
			{"HDD1", "Other/" + name, name},
		} {
			if err := indexAdd(entry.source, entry.path, entry.name, false, 123, 10); err != nil {
				t.Fatal(err)
			}
		}
		if err := indexAdd("HDD1", target, name, false, 456, 20); err != nil {
			t.Fatal(err)
		}
		for _, check := range []struct {
			source, path string
			size         int
		}{
			{"HDD1", target, 456},
			{"HDD2", target, 123},
			{"HDD1", "Other/" + name, 123},
		} {
			var count, size int
			err := db.QueryRow(`SELECT count(*), max(size) FROM files WHERE source=? AND path=?`, check.source, check.path).Scan(&count, &size)
			if err != nil || count != 1 || size != check.size {
				t.Fatalf("%q/%q: count=%d size=%d err=%v", check.source, check.path, count, size, err)
			}
		}
	}
	if indexedN != 6 {
		t.Fatalf("indexed count after replacing two paths: %d", indexedN)
	}
}

func TestPagedFileSearchAppliesDatesExtensionsAndPermissionsBeforeLimit(t *testing.T) {
	oldDB, oldIndexed, oldIndexing := db, indexedN, indexing
	if err := initSearch(filepath.Join(t.TempDir(), "index.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		db, indexedN, indexing = oldDB, oldIndexed, oldIndexing
	})
	start, end := searchYearBounds(2024, "Europe/Moscow")
	for i := 0; i < 28; i++ {
		name := fmt.Sprintf("sheet-%02d.XLSX", i)
		if _, err := db.Exec(`INSERT INTO files VALUES(?,?,'Disk',0,?,?)`, name, "Shared/"+name, 4096-i, start+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []struct {
		path string
		dir  int
		time int64
	}{
		{"Shared/old.xlsx", 0, start - 1},
		{"Shared/new.xlsx", 0, end},
		{"Shared/sheet.xlsx.bak", 0, start},
		{"Shared/directory.xlsx", 1, start},
		{"Private/secret.xlsx", 0, start},
	} {
		if _, err := db.Exec(`INSERT INTO files VALUES(?,?,'Disk',?,1024,?)`, filepath.Base(entry.path), entry.path, entry.dir, entry.time); err != nil {
			t.Fatal(err)
		}
	}
	access := &folderAccess{Folders: []folderGrant{{Source: "Disk", Path: "Shared"}}}
	search := func(query url.Values) searchResponse {
		t.Helper()
		query.Set("zone", "Europe/Moscow")
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/search?"+query.Encode(), nil)
		handleSearch(rr, req.WithContext(context.WithValue(req.Context(), profileKey{}, profileIdentity{folders: access})))
		var found searchResponse
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &found) != nil {
			t.Fatalf("file search %d: %s", rr.Code, rr.Body)
		}
		return found
	}
	for _, q := range []string{"xlsx 2024", ".XLSX 2024", "sheet .xlsx 2024"} {
		first := search(url.Values{"q": {q}, "mode": {"auto"}, "offset": {"0"}})
		if len(first.Results) != 25 || first.NextOffset != 25 || first.Intent == nil ||
			first.Intent.Extension != ".xlsx" || first.Intent.Year != 2024 || first.Indexed != 0 {
			t.Fatalf("%s first page: %+v", q, first)
		}
		next := search(url.Values{"q": {q}, "mode": {"auto"}, "offset": {"25"}})
		if next.Total != 28 || next.NextOffset != 0 || len(next.Results) != 3 {
			t.Fatalf("%s final page: %+v", q, next)
		}
		for i, result := range append(first.Results, next.Results...) {
			if result.Path != fmt.Sprintf("Shared/sheet-%02d.XLSX", i) || result.IsDir {
				t.Fatalf("%s row %d: %+v", q, i, result)
			}
		}
	}
	found := search(url.Values{
		"offset": {"0"}, "type": {"document"},
		"modifiedFrom": {strconv.FormatInt(start+3, 10)}, "modifiedBefore": {strconv.FormatInt(start+4, 10)},
	})
	if len(found.Results) != 1 || found.Results[0].Path != "Shared/sheet-03.XLSX" {
		t.Fatalf("calendar range without query: %+v", found)
	}
	found = search(url.Values{"q": {`".xlsx.bak"`}, "mode": {"auto"}, "offset": {"0"}})
	if found.Intent != nil || len(found.Results) != 1 || found.Results[0].Path != "Shared/sheet.xlsx.bak" {
		t.Fatalf("quoted search: %+v", found)
	}
	found = search(url.Values{"q": {"sheet"}, "mode": {"auto"}})
	if found.Total != 29 || found.NextOffset != 0 {
		t.Fatalf("legacy results: %+v", found)
	}
	for _, option := range []struct {
		key   string
		order string
	}{
		{"name", "desc"},
		{"size", "asc"},
		{"mtime", "desc"},
	} {
		query := url.Values{"q": {".xlsx 2024"}, "mode": {"auto"}, "offset": {"0"},
			"sort": {option.key}, "order": {option.order}}
		first := search(query)
		query.Set("offset", strconv.Itoa(first.NextOffset))
		next := search(query)
		results := append(first.Results, next.Results...)
		if len(results) != 28 || first.NextOffset != 25 || next.NextOffset != 0 {
			t.Fatalf("%+v returned %d results", option, len(results))
		}
		for i, result := range results {
			if result.Path != fmt.Sprintf("Shared/sheet-%02d.XLSX", 27-i) {
				t.Fatalf("%+v row %d: %+v", option, i, result)
			}
		}
	}
	for _, name := range []string{"Кириллица-Я.txt", "Кириллица-б.txt", "Кириллица-А.txt"} {
		if _, err := db.Exec(`INSERT INTO files VALUES(?,?,'Disk',0,1024,?)`, name, "Shared/"+name, start); err != nil {
			t.Fatal(err)
		}
	}
	found = search(url.Values{"q": {"кириллица"}, "offset": {"0"}, "sort": {"name"}})
	if len(found.Results) != 3 || found.Results[0].Name != "Кириллица-А.txt" ||
		found.Results[1].Name != "Кириллица-б.txt" || found.Results[2].Name != "Кириллица-Я.txt" {
		t.Fatalf("Cyrillic name sorting: %+v", found)
	}
}

func TestFileSearchFiltersBeforeLimitAndCount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index.db")
	oldDB, oldIndexed, oldIndexing := db, indexedN, indexing
	t.Cleanup(func() {
		if db != nil {
			db.Close()
		}
		db, indexedN, indexing = oldDB, oldIndexed, oldIndexing
	})
	if err := initSearch(dbPath); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 310; i++ {
		name := fmt.Sprintf("sample-%03d.txt", i)
		if _, err := tx.Exec(`INSERT INTO files VALUES(?, ?, 'A', 0, 1024, 90)`, name, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO files VALUES
		('sample-match-from-second-source.JPG', 'Photos/sample-match-from-second-source.JPG', 'B & C', 0, 2048, 100),
		('sample2.jpg', 'Photos/sample2.jpg', 'B & C', 0, 2048, 200),
		('sample3.png', 'Photos/sample3.png', 'B & C', 0, 4096, 100),
		('sample.pdf', 'Docs/sample.pdf', 'B & C', 0, 2048, 100),
		('sample folder.jpg', 'Photos/sample folder.jpg', 'B & C', 1, 2048, 100)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	indexedN, indexing = 315, true

	search := func(query url.Values) searchResponse {
		t.Helper()
		response := httptest.NewRecorder()
		handleSearch(response, httptest.NewRequest(http.MethodGet, "/api/search?"+query.Encode(), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status %d for %v: %s", response.Code, query, response.Body.String())
		}
		var found searchResponse
		if err := json.Unmarshal(response.Body.Bytes(), &found); err != nil {
			t.Fatal(err)
		}
		return found
	}

	found := search(url.Values{"q": {"sample"}})
	if found.Total != 315 || len(found.Results) != 300 || !found.Results[0].IsDir {
		t.Fatalf("unfiltered total=%d displayed=%d", found.Total, len(found.Results))
	}
	found = search(url.Values{"q": {"sample"}, "type": {"document"}})
	if found.Total != 311 || len(found.Results) != 300 {
		t.Fatalf("document total=%d displayed=%d", found.Total, len(found.Results))
	}
	found = search(url.Values{
		"q": {"sample"}, "source": {"B & C"}, "type": {"image"},
		"minSize": {"2048"}, "maxSize": {"2048"}, "modifiedFrom": {"100"}, "modifiedBefore": {"200"},
	})
	if found.Total != 1 || len(found.Results) != 1 || found.Results[0].Path != "Photos/sample-match-from-second-source.JPG" || found.Results[0].IsDir {
		t.Fatalf("combined filters: %+v", found)
	}
	if found.Indexed != 315 || !found.Indexing {
		t.Fatalf("filtered matches changed index state: %+v", found)
	}
	found = search(url.Values{"q": {"sample"}, "source": {"B & C"}, "type": {"folder"}})
	if found.Total != 1 || len(found.Results) != 1 || !found.Results[0].IsDir {
		t.Fatalf("folder filter: %+v", found)
	}
	found = search(url.Values{"q": {"sample"}, "source": {"missing"}})
	if found.Total != 0 || found.Results == nil || len(found.Results) != 0 || found.Indexed != 315 {
		t.Fatalf("empty filtered results: %+v", found)
	}
	found = search(nil)
	if found.Total != 0 || found.Results == nil || len(found.Results) != 0 || !found.Indexing || found.Indexed != 315 {
		t.Fatalf("index status without a filename: %+v", found)
	}
	const extraPath = "Photos/sample-extra.jpg"
	for _, size := range []int64{2048, 4096} {
		if err := indexAdd("B & C", extraPath, "sample-extra.jpg", false, size, 100); err != nil {
			t.Fatal(err)
		}
		if found := search(nil); found.Indexed != 316 {
			t.Fatalf("updating one index row yielded %d indexed entries", found.Indexed)
		}
	}
	if err := indexDelete("B & C", extraPath); err != nil {
		t.Fatal(err)
	}
	if found := search(nil); found.Indexed != 315 {
		t.Fatalf("deleting one index row yielded %d indexed entries", found.Indexed)
	}

	for key, value := range map[string]string{"type": "unknown", "minSize": "-1", "maxSize": "1.5", "modifiedBefore": "tomorrow"} {
		query := url.Values{"q": {"sample"}, key: {value}}
		response := httptest.NewRecorder()
		handleSearch(response, httptest.NewRequest(http.MethodGet, "/api/search?"+query.Encode(), nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid parameter %s: status %d", key, response.Code)
		}
	}
}

func TestNaturalFileSearchFiltersBeforeProfileCounts(t *testing.T) {
	oldDB, oldIndexed, oldIndexing := db, indexedN, indexing
	t.Cleanup(func() {
		if db != nil {
			db.Close()
		}
		db, indexedN, indexing = oldDB, oldIndexed, oldIndexing
	})
	if err := initSearch(filepath.Join(t.TempDir(), "index.db")); err != nil {
		t.Fatal(err)
	}
	edge := time.Date(2024, time.December, 31, 21, 0, 0, 0, time.UTC).Unix()
	for _, file := range []struct {
		path  string
		mtime int64
	}{
		{"Family/2025.png", edge},
		{"Family/older.png", edge - 1},
		{"Family/test.mp4", edge},
		{"Family/test.mp4.bak", edge - 1},
		{"Family/test.AVI", edge - 1},
		{"Private/2025.png", edge},
		{"Private/report-2025.pdf", edge},
	} {
		if _, err := db.Exec(`INSERT INTO files VALUES(?,?,'Library',0,1024,?)`, filepath.Base(file.path), file.path, file.mtime); err != nil {
			t.Fatal(err)
		}
	}
	search := func(query url.Values, access *folderAccess) searchResponse {
		t.Helper()
		query.Set("zone", "Europe/Moscow")
		req := httptest.NewRequest(http.MethodGet, "/api/search?"+query.Encode(), nil)
		req = req.WithContext(context.WithValue(req.Context(), profileKey{}, profileIdentity{folders: access}))
		recorder := httptest.NewRecorder()
		handleSearch(recorder, req)
		var found searchResponse
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &found) != nil {
			t.Fatalf("file search %d: %s", recorder.Code, recorder.Body)
		}
		return found
	}
	family := &folderAccess{Folders: []folderGrant{{Source: "Library", Path: "Family"}}}
	found := search(url.Values{"q": {"фото 2025"}, "mode": {"auto"}}, family)
	if found.Total != 1 || found.Results[0].Path != "Family/2025.png" || found.Intent == nil ||
		found.Intent.Type != "image" || found.Intent.Year != 2025 || found.Intent.WithoutType != "2025" || found.Intent.WithoutYear != "фото" {
		t.Fatalf("scoped year and photo filter: %+v", found)
	}
	if found := search(url.Values{"q": {"photos 2025"}, "mode": {"auto"}}, nil); found.Total != 2 {
		t.Fatalf("administrator photo count: %+v", found)
	}
	if found := search(url.Values{"q": {"Family photos 2025"}, "mode": {"auto"}}, nil); found.Total != 1 ||
		found.Results[0].Path != "Family/2025.png" {
		t.Fatalf("path search with inferred filters: %+v", found)
	}
	if found := search(url.Values{"q": {"видео"}, "mode": {"auto"}}, family); found.Total != 2 ||
		found.Results[0].Path != "Family/test.mp4" || found.Results[1].Path != "Family/test.AVI" {
		t.Fatalf("type-only search: %+v", found)
	}
	if found := search(url.Values{"q": {"videos"}, "mode": {"auto"}, "type": {"image"}}, family); found.Total != 0 {
		t.Fatalf("incompatible inferred and manual filters: %+v", found)
	}
	if found := search(url.Values{"q": {"2025 год"}, "mode": {"auto"}}, family); found.Total != 2 {
		t.Fatalf("year-only search: %+v", found)
	}
	if found := search(url.Values{"q": {`"2025"`}, "mode": {"auto"}}, nil); found.Total != 3 || found.Intent != nil {
		t.Fatalf("quoted name search: %+v", found)
	}
	if found := search(url.Values{"q": {"2025"}, "mode": {"literal"}, "type": {"document"}}, nil); found.Total != 1 || found.Intent != nil {
		t.Fatalf("literal name search: %+v", found)
	}
}
