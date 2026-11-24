package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Runs the production Kotlin uploader with file-backed platform adapters. This
// tests its HTTP/checksum/retry logic; Android scheduling and UI need Android.
func TestAndroidUploaderFaultRecovery(t *testing.T) {
	cp := os.Getenv("FILEHUB_UPLOADER_TEST_CLASSPATH")
	if cp == "" {
		t.Skip("set FILEHUB_UPLOADER_TEST_CLASSPATH to the JVM harness classpath")
	}
	for _, mode := range []string{"fresh", "lost-final", "partial", "changed", "existing", "old-server"} {
		t.Run(mode, func(t *testing.T) {
			f := newReceiptFixture(t)
			input := filepath.Join(t.TempDir(), "source.bin")
			payload := bytes.Repeat([]byte("filehub-transfer"), 1200000)
			if err := os.WriteFile(input, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if mode == "existing" {
				if err := os.WriteFile(filepath.Join(f.root, "phone.mp4"), payload, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var injected atomic.Bool
			var creates atomic.Int32
			var patches atomic.Int32
			var probes atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == preflightURL {
					probes.Add(1)
					if mode == "old-server" {
						http.NotFound(w, r)
						return
					}
				}
				if r.Method == "POST" && r.URL.Path == "/files/" {
					creates.Add(1)
				}
				first := r.Method == "PATCH" && injected.CompareAndSwap(false, true)
				if r.Method == "PATCH" {
					patches.Add(1)
				}
				if mode == "partial" && first {
					r.Body = io.NopCloser(io.LimitReader(r.Body, 4*1024*1024))
					r.ContentLength = 4 * 1024 * 1024
				}
				rr := httptest.NewRecorder()
				f.mux.ServeHTTP(rr, r)
				if mode == "changed" && first {
					replacement := bytes.Repeat([]byte("changed-content"), 1200000)
					os.WriteFile(input, replacement, 0o600)
					now := time.Now().Add(time.Second)
					os.Chtimes(input, now, now)
				}
				if mode == "lost-final" && rr.Header().Get("X-Filehub-SHA256") != "" && !injected.Swap(true) {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						conn.Close()
					}
					return
				}
				// For lost-final, injected is reserved for the completed response.
				for k, v := range rr.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(rr.Code)
				w.Write(rr.Body.Bytes())
			})
			if mode == "lost-final" {
				handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" && r.URL.Path == "/files/" {
						creates.Add(1)
					}
					if r.Method == "PATCH" {
						patches.Add(1)
					}
					rr := httptest.NewRecorder()
					f.mux.ServeHTTP(rr, r)
					if rr.Header().Get("X-Filehub-SHA256") != "" && injected.CompareAndSwap(false, true) {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							conn.Close()
						}
						return
					}
					for k, v := range rr.Header() {
						w.Header()[k] = v
					}
					w.WriteHeader(rr.Code)
					w.Write(rr.Body.Bytes())
				})
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "java", "-cp", cp, "com.filehub.backup.HarnessKt", input, mode)
			cmd.Env = append(os.Environ(), "FILEHUB_TEST_URL="+server.URL, "FILEHUB_TEST_COOKIE="+signSession("writer"))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Kotlin uploader: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "PASS "+mode) {
				t.Fatalf("missing success: %s", output)
			}
			t.Log(strings.TrimSpace(string(output)))
			if mode != "changed" {
				b, e := os.ReadFile(filepath.Join(f.root, "phone.mp4"))
				if e != nil || !bytes.Equal(b, payload) {
					t.Fatalf("saved content mismatch: %v", e)
				}
				if mode == "existing" {
					if probes.Load() != 2 || creates.Load() != 0 || patches.Load() != 0 {
						t.Fatalf("existing copy sent a body: probes=%d creates=%d patches=%d", probes.Load(), creates.Load(), patches.Load())
					}
				} else if creates.Load() != 1 {
					t.Fatalf("created %d uploads, want 1", creates.Load())
				}
				entries, _ := os.ReadDir(f.root)
				if len(entries) != 1 {
					t.Fatalf("created %d copies", len(entries))
				}
				if mode == "lost-final" && patches.Load() != 3 {
					t.Fatalf("resent data after lost final response: %d PATCHes", patches.Load())
				}
			}
		})
	}
}
