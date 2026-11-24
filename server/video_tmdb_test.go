package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTMDBSelectsOnlyUniqueExactMatch(t *testing.T) {
	matches := []tmdbRecord{
		{ID: 1, Name: "Бесстыжие", Original: "Shameless", Year: 2004},
		{ID: 2, Name: "Бесстыжие", Original: "Shameless", Year: 2011},
		{ID: 3, Name: "Shameless Reunion", Year: 2011},
	}
	if result := tmdbExactMatch("Shameless", 0, matches); result.ID != 0 {
		t.Fatal("picked a remake without distinguishing the release year")
	}
	if result := tmdbExactMatch("Бесстыжие", 2011, matches); result.ID != 2 {
		t.Fatalf("unexpected match: %+v", result)
	}
	if result := tmdbExactMatch("Shameless", 2020, matches); result.ID != 0 {
		t.Fatal("picked a match from the wrong year")
	}
	name, year := tmdbQueryFor(&videoTitle{Name: "Blade Runner 2049 2017 1080p BluRay"})
	if name != "Blade Runner 2049" || year != 2017 {
		t.Fatalf("wrong metadata query: %q, %d", name, year)
	}
	name, year = tmdbQueryFor(&videoTitle{Name: "Blade Runner 2049"})
	if name != "Blade Runner 2049" || year != 0 {
		t.Fatalf("part of the title was interpreted as a year: %q, %d", name, year)
	}
}

func TestTMDBManualMatchPersistsAndPreservesLocalCover(t *testing.T) {
	mux := authSecurityFixture(t).(*http.ServeMux)
	if err := registerVideoLibrary(mux); err != nil {
		t.Fatal(err)
	}
	admin := securityLogin(t, mux, "admin", authTestOldPassword)
	writer := securityLogin(t, mux, "writer", authTestOldPassword)

	dir := filepath.Join(sources[0].Root, "Movies")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Example.mkv", "poster.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO video_folders VALUES('T','Movies','movies')"); err != nil {
		t.Fatal(err)
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/search/movie":
			if r.URL.Query().Get("query") != "Example" || r.URL.Query().Get("primary_release_year") != "2011" {
				t.Errorf("wrong upstream movie search: %s", r.URL)
			}
			w.Write([]byte(`{"results":[{"id":17,"title":"Example","release_date":"2011-01-01","poster_path":"//untrusted.invalid/d.jpg"}]}`))
		case "/movie/17":
			w.Write([]byte(`{"id":17,"title":"Example","release_date":"2011-01-01","poster_path":"/fixture.jpg","overview":"Synopsis","genres":[{"name":"Drama"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()
	oldToken, oldBase := libraryTMDB.token, libraryTMDB.base
	libraryTMDB.token, libraryTMDB.base = "synthetic", mock.URL
	t.Cleanup(func() { libraryTMDB.token, libraryTMDB.base = oldToken, oldBase })
	id := "1:T/movies/Movies/Example.mkv"
	query := url.Values{"title": {id}, "q": {"Example"}, "year": {"2011"}}
	search := securityRequest(mux, "GET", "/api/admin/video/tmdb/search?"+query.Encode(), admin, nil)
	var matches []tmdbRecord
	if search.Code != 200 || json.Unmarshal(search.Body.Bytes(), &matches) != nil ||
		len(matches) != 1 || matches[0].Poster != "" {
		t.Fatalf("unsafe or missing search result: %d %s", search.Code, search.Body)
	}
	form := url.Values{"title": {id}, "match": {"17"}}
	if res := securityRequest(mux, "POST", "/api/admin/video/tmdb/match", writer, form); res.Code != 403 {
		t.Fatalf("writer could choose a match: %d", res.Code)
	}
	if res := securityRequest(mux, "POST", "/api/admin/video/tmdb/match", admin, form); res.Code != 204 {
		t.Fatalf("could not save match: %d %s", res.Code, res.Body)
	}
	res := securityRequest(mux, "GET", "/api/video/library", admin, nil)
	var catalog struct {
		Titles []videoTitle `json:"titles"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &catalog) != nil || len(catalog.Titles) != 1 {
		t.Fatalf("could not reload library: %d %s", res.Code, res.Body)
	}
	title := catalog.Titles[0]
	if title.TMDBMatchID != 17 || title.Poster != tmdbImages+"/fixture.jpg" ||
		title.Cover != "Movies/poster.jpg" || title.Description != "Synopsis" || title.Year != 2011 {
		t.Fatalf("incomplete persisted match: %+v", title)
	}
}

func TestDarkVideoFrameRejectsMostlyBlackFrames(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 48, 32))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	draw.Draw(frame, image.Rect(0, 0, 8, 8), image.NewUniform(color.White), image.Point{}, draw.Src)
	if !darkVideoFrame(frame) {
		t.Fatal("mostly black frame with a small mark was accepted")
	}
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.RGBA{R: 40, G: 100, B: 170, A: 255}), image.Point{}, draw.Src)
	if darkVideoFrame(frame) {
		t.Fatal("ordinary video frame was marked dark")
	}
}

func TestTMDBSettingsAcceptV3AndTokenWithoutReturningSecret(t *testing.T) {
	mux := authSecurityFixture(t).(*http.ServeMux)
	if err := registerVideoLibrary(mux); err != nil {
		t.Fatal(err)
	}
	admin := securityLogin(t, mux, "admin", authTestOldPassword)
	writer := securityLogin(t, mux, "writer", authTestOldPassword)
	const key = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const token = "synthetic.read.token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configuration" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("api_key") != key && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "invalid key", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"images":{"secure_base_url":"https://image.tmdb.org/t/p/"}}`))
	}))
	defer upstream.Close()
	oldFile, oldBase := libraryTMDB.credentialFile, libraryTMDB.base
	libraryTMDB.credentialFile, libraryTMDB.base = filepath.Join(t.TempDir(), ".filehub-tmdb.json"), upstream.URL
	t.Cleanup(func() { libraryTMDB.credentialFile, libraryTMDB.base = oldFile, oldBase })
	endpoint := "/api/admin/video/tmdb/settings"
	payload := func(credential string) url.Values {
		return url.Values{"action": {"save"}, "credential": {credential}}
	}
	if response := securityRequest(mux, "POST", endpoint, writer, payload(key)); response.Code != http.StatusForbidden {
		t.Fatalf("writer could configure TMDB: %d", response.Code)
	}
	for _, credential := range []string{key, token} {
		response := securityRequest(mux, "POST", endpoint, admin, payload(credential))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), credential) {
			t.Fatalf("unexpected save response or exposed credential: %d", response.Code)
		}
		info, err := os.Stat(libraryTMDB.credentialFile)
		if err != nil || info.Mode().Perm() != 0o600 || libraryTMDB.credential() != credential {
			t.Fatal("credential was not stored with the expected permissions")
		}
		status := securityRequest(mux, "GET", endpoint, admin, nil)
		if status.Code != http.StatusOK || strings.TrimSpace(status.Body.String()) != `{"configured":true}` {
			t.Fatalf("status must return only configuration state: %d", status.Code)
		}
	}
	if response := securityRequest(mux, "POST", endpoint, admin, payload(strings.Repeat("f", 32))); response.Code != http.StatusUnprocessableEntity || libraryTMDB.credential() != token {
		t.Fatal("a rejected credential replaced the working one")
	}
	if response := securityRequest(mux, "POST", endpoint, admin, url.Values{"action": {"disable"}}); response.Code != http.StatusOK || libraryTMDB.credential() != "" {
		t.Fatal("TMDB did not disconnect")
	}
}
