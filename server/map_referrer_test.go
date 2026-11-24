package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMapOriginPolicyDoesNotExposePrivateLinks(t *testing.T) {
	public := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/", webReferrerPolicy(public))
	for _, prefix := range []string{"/api/", "/s/", "/a/", "/play/", "/dav/"} {
		mux.Handle(prefix, public)
	}
	handler := securityMiddleware(mux)
	for _, path := range []string{
		"/", "/index.html?query=private", "/assets/index.js",
		"/assets/maplibre-gl-worker.mjs",
		"/api/photos/places", "/api/maps/config",
		"/api/download?source=HDD&path=private/file.jpg",
		"/s/private-share-token", "/a/private-album-token",
		"/play/private-stream-token", "/dav/private/file.jpg",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			want := "strict-origin-when-cross-origin"
			if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/s/") ||
				strings.HasPrefix(path, "/a/") || strings.HasPrefix(path, "/play/") || strings.HasPrefix(path, "/dav/") {
				want = "no-referrer"
			}
			if got := response.Header().Get("Referrer-Policy"); got != want {
				t.Fatalf("Referrer-Policy = %q, want %q", got, want)
			}
			if response.Code != http.StatusOK || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("unrelated response protection changed", response.Code, response.Header())
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/api/password", nil)
	request.Header.Set("Origin", "https://different-origin.invalid")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("cross-origin mutation protection changed", response.Code, response.Header())
	}
}
