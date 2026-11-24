package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedWebAssetHeaders(t *testing.T) {
	handler := embeddedWebAssets(fstest.MapFS{
		"index.html":                &fstest.MapFile{Data: []byte("<html>")},
		"assets/app-abcdefgh.js":    &fstest.MapFile{Data: []byte("plain")},
		"assets/app-abcdefgh.js.gz": &fstest.MapFile{Data: []byte("compressed")},
	})
	for _, tc := range []struct{ file, accept, wantEncoding, wantCache, wantBody string }{
		{"/assets/app-abcdefgh.js", "br, gzip", "gzip", "public, max-age=31536000, immutable", "compressed"},
		{"/assets/app-abcdefgh.js", "*;q=0.5, gzip;q=0", "", "public, max-age=31536000, immutable", "plain"},
		{"/", "gzip", "", "no-cache", "<html>"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.file, nil)
		req.Header.Set("Accept-Encoding", tc.accept)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != tc.wantEncoding ||
			w.Header().Get("Cache-Control") != tc.wantCache || w.Body.String() != tc.wantBody {
			t.Fatalf("%+v got status %d headers %v body %q", tc, w.Code, w.Header(), w.Body.String())
		}
	}
}

func TestEmbeddedGzipMatchesFinalWebAssets(t *testing.T) {
	assets, err := fs.Glob(webFS, "web/assets/*.gz")
	if err != nil || len(assets) == 0 {
		t.Fatalf("expected compressed build assets, got %v (%v)", assets, err)
	}
	for _, asset := range assets {
		t.Run(asset, func(t *testing.T) {
			compressed, err := webFS.ReadFile(asset)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			decoded, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			original, err := webFS.ReadFile(strings.TrimSuffix(asset, ".gz"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded, original) {
				t.Fatal("gzip does not match the final bundled asset")
			}
		})
	}
}
