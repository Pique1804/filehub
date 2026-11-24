package main

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var hashedWebAsset = regexp.MustCompile(`^/assets/[^/]+-[A-Za-z0-9_-]{8}\.(?:js|css)$`)

func acceptsGzip(value string) bool {
	wildcard := false
	for _, item := range strings.Split(value, ",") {
		kind, parameters, _ := strings.Cut(item, ";")
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind != "gzip" && kind != "*" {
			continue
		}
		quality := 1.0
		if key, weight, ok := strings.Cut(strings.TrimSpace(parameters), "="); ok && strings.TrimSpace(key) == "q" {
			quality, _ = strconv.ParseFloat(strings.TrimSpace(weight), 64)
		}
		if kind == "gzip" {
			return quality > 0 && quality <= 1
		}
		wildcard = quality > 0 && quality <= 1
	}
	return wildcard
}

func embeddedWebAssets(files fs.FS) http.Handler {
	base := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := r.URL.Path
		if strings.HasSuffix(requestPath, ".gz") && hashedWebAsset.MatchString(strings.TrimSuffix(requestPath, ".gz")) {
			http.NotFound(w, r)
			return
		}
		if hashedWebAsset.MatchString(requestPath) {
			if _, err := fs.Stat(files, strings.TrimPrefix(requestPath, "/")); err == nil {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				w.Header().Add("Vary", "Accept-Encoding")
				if acceptsGzip(strings.Join(r.Header.Values("Accept-Encoding"), ",")) {
					data, err := fs.ReadFile(files, strings.TrimPrefix(requestPath, "/")+".gz")
					if err == nil {
						w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(requestPath)))
						w.Header().Set("Content-Encoding", "gzip")
						http.ServeContent(w, r, path.Base(requestPath), time.Time{}, bytes.NewReader(data))
						return
					}
				}
			}
		} else if requestPath == "/" || strings.HasSuffix(requestPath, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		base.ServeHTTP(w, r)
	})
}
