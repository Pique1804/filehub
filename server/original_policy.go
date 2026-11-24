package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func protectedRelativePath(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/") {
		if strings.HasPrefix(part, ".filehub-") {
			return true
		}
	}
	return false
}

// Originals are untrusted documents, even when their extension is familiar.
// Apply this only to file bytes, never to the application's own HTML.
func originalHeaders(w http.ResponseWriter, full string, inline bool) {
	contentType := mediaContentType(full)
	baseType, _, _ := mime.ParseMediaType(contentType)
	safe := false
	switch baseType {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/avif", "image/bmp", "image/heic", "image/heif", "application/pdf", "text/plain":
		safe = true
	default:
		safe = strings.HasPrefix(baseType, "audio/") || strings.HasPrefix(baseType, "video/")
	}
	disposition := "attachment"
	if inline && safe {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(full)}))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	policy := "sandbox; default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	if inline && baseType == "application/pdf" {
		// Native browser PDF viewers are disabled by sandboxed plugin policy.
		// nosniff plus the explicit PDF MIME keeps an HTML payload from becoming
		// an application-origin document while preserving the existing preview.
		policy = "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
	}
	w.Header().Set("Content-Security-Policy", policy)
}

// Resolve aliases before applying the private-name policy. Missing leaves are
// checked through their nearest existing parent, as in ordinary upload paths.
func publicPathInside(root, full string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	for probe := full; ; probe = filepath.Dir(probe) {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			rel, err := filepath.Rel(resolvedRoot, resolved)
			return err == nil && filepath.IsLocal(rel) && !protectedRelativePath(rel)
		}
		if !os.IsNotExist(err) || filepath.Dir(probe) == probe {
			return false
		}
	}
}
