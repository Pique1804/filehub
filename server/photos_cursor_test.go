package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestVisualSearchCancelsTheSidecarRequest(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(stopped)
		case <-time.After(4 * time.Second):
		}
	}))
	defer model.Close()
	t.Setenv("FILEHUB_CLIP_SIDECAR", model.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := queryVector(ctx, "synthetic scene")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the visual search did not reach the sidecar")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the sidecar request was not cancelled")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestPhotoListMatchesOnlySupportedExtensions(t *testing.T) {
	maintenanceFixture(t)
	input := []struct {
		path string
		dir  int
	}{
		{"Camera/photo.JPG", 0},
		{"Camera/screenshot.HEIC", 0},
		{"Camera/film.mKv", 0},
		{"Camera/photo.jpg.txt", 0},
		{"Camera/directory.png", 1},
		{"Other/elsewhere.jpeg", 0},
	}
	for _, row := range input {
		_, err := db.Exec(`INSERT INTO files VALUES(?,?,'T',?,1024,10)`, filepath.Base(row.path), row.path, row.dir)
		if err != nil {
			t.Fatal(err)
		}
	}
	list := buildPhotoList("T", "Camera")
	if len(list) != 3 || list[0].Path != "Camera/film.mKv" || !list[0].IsVideo ||
		list[1].Path != "Camera/photo.JPG" || list[2].Path != "Camera/screenshot.HEIC" {
		t.Fatalf("unexpected filtered photo list: %+v", list)
	}
}

func TestPhotoCursorStableWhenNewerFilesArrive(t *testing.T) {
	list := []Photo{{Source: "b", Path: "a.jpg", ModTime: 10}, {Source: "a", Path: "b.jpg", ModTime: 10}, {Source: "a", Path: "a.jpg", ModTime: 10}, {Source: "a", Path: "old.jpg", ModTime: 9}}
	sort.Slice(list, func(i, j int) bool { return photoBefore(list[i], list[j]) })
	last, err := decodePhotoCursor(encodePhotoCursor(list[1]))
	if err != nil {
		t.Fatal(err)
	}
	list = append([]Photo{{Source: "a", Path: "new.jpg", ModTime: 11}}, list...)
	start := sort.Search(len(list), func(i int) bool { return photoBefore(last, list[i]) })
	if len(list[start:]) != 2 || list[start].Source != "b" || list[start+1].Path != "old.jpg" {
		t.Fatalf("skipped/repeated items: %+v", list[start:])
	}
	for _, raw := range []string{"!bad", "e30", strings.Repeat("a", 8193)} {
		if _, err := decodePhotoCursor(raw); err == nil {
			t.Fatalf("accepted %q", raw[:3])
		}
	}
}
func TestMapStyleRejectsMalformedProvider(t *testing.T) {
	for _, url := range []string{"file:///tmp/{z}/{x}/{y}", "https://user:secret@tiles.test/{z}/{x}/{y}", "https://tiles.test/{x}/{y}", "javascript:alert(1)"} {
		if _, ok := mapStyle(url, "provider"); ok {
			t.Fatal("accepted", url)
		}
	}
	style, ok := mapStyle("https://tile.openstreetmap.org/{z}/{x}/{y}.png", "<script>provider</script>")
	if !ok || style["version"] != 8 {
		t.Fatal(style)
	}
	if got := style["sources"].(map[string]any)["basemap"].(map[string]any)["attribution"]; got != "&lt;script&gt;provider&lt;/script&gt;" {
		t.Fatal(got)
	}
}
