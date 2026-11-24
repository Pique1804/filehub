package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MIME sniffing distinguishes MP4 files incorrectly named .jpg from real photos.
// HEIC's ISO-BMFF header is deliberately not assumed to be a video.
func mediaKind(full string) (bool, string) {
	fallback := isVideoThumb(full)
	f, e := os.Open(full)
	if e != nil {
		return fallback, ""
	}
	defer f.Close()
	data := make([]byte, 512)
	n, _ := f.Read(data)
	mime := http.DetectContentType(data[:n])
	if strings.HasPrefix(mime, "video/") {
		return true, mime
	}
	if strings.HasPrefix(mime, "image/") {
		return false, mime
	}
	return fallback, mime
}
func actualVideoFile(full string) bool { video, _ := mediaKind(full); return video }
func initMediaTypes() error {
	_, e := db.Exec(`CREATE TABLE IF NOT EXISTS media_types(source TEXT,path TEXT,size INTEGER,mtime_ns INTEGER,is_video INTEGER,mime TEXT,PRIMARY KEY(source,path))`)
	return e
}
func indexMediaType(source, path, full string) error {
	if !tableExists("media_types") {
		return nil
	}
	st, e := os.Stat(full)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM media_types WHERE source=? AND path=? AND size=? AND mtime_ns=?`, source, path, st.Size(), st.ModTime().UnixNano()).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	video, mime := mediaKind(full)
	after, e := os.Stat(full)
	if e != nil || after.Size() != st.Size() || after.ModTime() != st.ModTime() {
		return os.ErrInvalid
	}
	_, e = db.Exec(`INSERT INTO media_types VALUES(?,?,?,?,?,?) ON CONFLICT(source,path) DO UPDATE SET size=excluded.size,mtime_ns=excluded.mtime_ns,is_video=excluded.is_video,mime=excluded.mime`, source, path, st.Size(), st.ModTime().UnixNano(), video, mime)
	if e == nil && video != isVideoThumb(path) {
		invalidateMediaCaches()
	}
	return e
}
func mediaTypeMap() map[string]bool {
	out := map[string]bool{}
	if !tableExists("media_types") {
		return out
	}
	rows, e := db.Query(`SELECT source,path,is_video FROM media_types`)
	if e != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s, p string
		var v bool
		if rows.Scan(&s, &p, &v) == nil {
			out[s+"|"+p] = v
		}
	}
	return out
}
func photoVideo(types map[string]bool, source, path, name string) bool {
	if v, ok := types[source+"|"+path]; ok {
		return v
	}
	return isVideoThumb(name)
}
func mediaTypeRevision(types map[string]bool, source, path, name string) string {
	if video, ok := types[source+"|"+path]; ok && video != isVideoThumb(name) {
		if video {
			return "-video"
		}
		return "-image"
	}
	return ""
}
func mediaTypeSweep() {
	// Files are read a header at a time; existing fingerprints are skipped.
	for _, p := range cachedAllPhotos() {
		if full, ok := safePath(p.Source, p.Path); ok {
			indexMediaType(p.Source, p.Path, full)
		}
	}
	invalidateMediaCaches()
}
func mediaContentType(full string) string {
	_, detected := mediaKind(full)
	if strings.HasPrefix(detected, "image/") || strings.HasPrefix(detected, "video/") {
		return detected
	}
	if known := mime.TypeByExtension(filepath.Ext(full)); known != "" {
		return known
	}
	return detected
}
