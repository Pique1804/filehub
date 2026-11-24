package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVisualSearchSettingsPersistenceAndFileRevisions(t *testing.T) {
	root := maintenanceFixture(t)
	t.Setenv("FILEHUB_CLIP_SIDECAR", "")
	fresh, err := newVisualSearch()
	if err != nil || fresh.run != nil {
		t.Fatalf("fresh configuration: %v, %v", fresh, err)
	}
	vec := make([]float32, 512)
	vec[0] = 1
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(200)
			return
		}
		writeJSON(w, map[string]any{"vec": vec})
	}))
	defer model.Close()
	t.Setenv("FILEHUB_CLIP_SIDECAR", model.URL)
	t.Setenv("FILEHUB_CLIP_DEFAULT_ENABLED", "0")
	configured, err := newVisualSearch()
	if err != nil || configured.run != nil {
		t.Fatal("new installation with a preconfigured local model must start disabled", err)
	}
	t.Setenv("FILEHUB_CLIP_DEFAULT_ENABLED", "")
	s, err := newVisualSearch()
	if err != nil || s.run == nil {
		t.Fatal("legacy configured sidecar was disabled", err)
	}
	operation := s.operation(t.Context())
	defer operation.cancel()
	set := func(value string) {
		t.Helper()
		w := httptest.NewRecorder()
		s.setEnabled(w, httptest.NewRequest("POST", "/", strings.NewReader(value)))
		if w.Code != 204 {
			t.Fatalf("save status %d: %s", w.Code, w.Body)
		}
	}
	set(`{"enabled":false}`)
	select {
	case <-operation.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("disabling did not cancel the current operation")
	}
	again, err := newVisualSearch()
	if err != nil || again.run != nil {
		t.Fatal("disabled preference not persisted", err)
	}
	set(`{"enabled":true}`)

	for _, folder := range []string{"visible", "private"} {
		if err := os.Mkdir(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
		path := folder + "/item.jpg"
		full := filepath.Join(root, path)
		if err := os.WriteFile(full, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := indexAddPath("T", full, path); err != nil {
			t.Fatal(err)
		}
		_, info, ok := visualFile("T", path)
		if !ok || !s.store(t.Context(), "T", path, photoFileStamp(info), vec) {
			t.Fatalf("could not index %s", path)
		}
	}
	reader := profileIdentity{role: "viewer", folders: &folderAccess{Folders: []folderGrant{{Source: "T", Path: "visible"}}}}
	results := func(profile profileIdentity) []Photo {
		t.Helper()
		r := withProfile(httptest.NewRequest(http.MethodGet, "/api/search/semantic", nil), profile)
		out, _, err := rankVisualPhotos(r, vec, 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if result := results(reader); len(result) != 1 || result[0].Path != "visible/item.jpg" {
		t.Fatalf("scoped result: %#v", result)
	}
	path := filepath.Join(root, "visible/item.jpg")
	_, original, _ := visualFile("T", "visible/item.jpg")
	stamp := photoFileStamp(original)
	temp := filepath.Join(root, "replacement.jpg")
	if err := os.WriteFile(temp, []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(temp, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, path); err != nil {
		t.Fatal(err)
	}
	if s.store(t.Context(), "T", "visible/item.jpg", stamp, vec) || len(results(reader)) != 0 {
		t.Fatal("an embedding was reused after replacing the original")
	}
	_, replacement, _ := visualFile("T", "visible/item.jpg")
	if !s.store(t.Context(), "T", "visible/item.jpg", photoFileStamp(replacement), vec) {
		t.Fatal("could not index replacement")
	}
	reader.folders = &folderAccess{Folders: []folderGrant{}}
	if len(results(reader)) != 0 {
		t.Fatal("an indexed file remained visible after its grant was removed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reader.folders = &folderAccess{Folders: []folderGrant{{Source: "T", Path: "visible"}}}
	if len(results(reader)) != 0 {
		t.Fatal("a deleted indexed file remained visible")
	}
	set(`{"enabled":false}`)
	w := httptest.NewRecorder()
	s.search(w, httptest.NewRequest("GET", "/api/search/semantic?q=test", nil))
	if w.Code != 503 {
		t.Fatalf("disabled visual search responded %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.todo(w, httptest.NewRequest("GET", "/api/embed/todo", nil))
	var pending struct{ Items []json.RawMessage }
	if json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&pending) != nil || len(pending.Items) != 0 {
		t.Fatalf("disabled sidecar received work: %s", w.Body)
	}
}

func TestVisualTextQueryRetriesWhenModelIsBusy(t *testing.T) {
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		vec := make([]float32, 512)
		vec[0] = 1
		writeJSON(w, map[string]any{"vec": vec})
	}))
	defer model.Close()
	t.Setenv("FILEHUB_CLIP_SIDECAR", model.URL)
	vec, err := queryVector(t.Context(), "кот")
	if err != nil || len(vec) != 512 || calls.Load() != 2 {
		t.Fatalf("query failed after model became free: %v, calls=%d", err, calls.Load())
	}
}

func TestVisualSearchPaginatesOnlyRelevantVisibleMatches(t *testing.T) {
	root := maintenanceFixture(t)
	s, err := newVisualSearch()
	if err != nil {
		t.Fatal(err)
	}
	s.run = t.Context()
	vector := make([]float32, 512)
	vector[0] = 1
	weak := make([]float32, 512)
	weak[0], weak[1] = 0.24, 0.97
	for _, folder := range []string{"shared", "private"} {
		if err := os.Mkdir(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 95; i++ {
		folder := "shared"
		embedding := vector
		if i >= 75 {
			embedding = weak
		} else if i%6 == 0 {
			folder = "private"
		}
		path := fmt.Sprintf("%s/%02d.jpg", folder, i)
		full := filepath.Join(root, path)
		if err := os.WriteFile(full, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := indexAddPath("T", full, path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(full)
		if err != nil || !s.store(t.Context(), "T", path, photoFileStamp(info), embedding) {
			t.Fatalf("index %s: %v", path, err)
		}
	}
	profile := profileIdentity{role: "viewer", folders: &folderAccess{Folders: []folderGrant{{Source: "T", Path: "shared"}}}}
	r := withProfile(httptest.NewRequest("GET", "/api/search/semantic", nil), profile)
	page, next, err := rankVisualPhotos(r, vector, 60, nil)
	if err != nil || len(page) != 60 || next == "" {
		t.Fatalf("first page: %d entries, cursor %q, error %v", len(page), next, err)
	}
	after, ok := readVisualCursor(next)
	if !ok {
		t.Fatal("cursor does not round-trip")
	}
	rest, end, err := rankVisualPhotos(r, vector, 60, &after)
	if err != nil || len(rest) != 2 || end != "" {
		t.Fatalf("last page: %d entries, cursor %q, error %v", len(rest), end, err)
	}
	last := ""
	for _, p := range append(page, rest...) {
		if p.Path <= last || !strings.HasPrefix(p.Path, "shared/") {
			t.Fatalf("unstable or unscoped result: %s after %s", p.Path, last)
		}
		last = p.Path
	}
}
