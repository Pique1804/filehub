// Photo timeline: a Google-Photos / Immich-style newest-first feed of all image
// and video files, sourced from the existing FTS index (no filesystem walk) so
// it stays fast even on a 500k-entry library. Grouping by date is done client-side.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Photo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Source   string `json:"source"`
	ModTime  int64  `json:"modTime"`
	Size     int64  `json:"size"`
	IsVideo  bool   `json:"isVideo"`
	Revision string `json:"revision"`
}

const photosCap = 50000  // maximum size of a single legacy timeline response
const prewarmCap = 10000 // prewarm the WHOLE scoped timeline (~8k) — un-prewarmed photos generate on-demand at 0.5-6s/cell = the grid stutter

// Phone photos/screenshots embed the real capture date in the filename
// (20251127_171929, Screenshot_20260612_041730_…, IMG_20240315, 2024-03-15…).
// File mtime is just the upload/copy time (all "June"), so we prefer the
// filename date for the timeline; falling back to mtime when none is found.
var fnDateRe = regexp.MustCompile(`(19|20)(\d{2})[-_.]?(\d{2})[-_.]?(\d{2})(?:[-_ tT]?(\d{2})[-:.]?(\d{2})[-:.]?(\d{2}))?`)

func captureDate(name string, mtime int64) int64 {
	m := fnDateRe.FindStringSubmatch(name)
	if m == nil {
		return mtime
	}
	ai := func(s string) int { n, _ := strconv.Atoi(s); return n }
	year := ai(m[1])*100 + ai(m[2])
	mon, day := ai(m[3]), ai(m[4])
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		return mtime
	}
	t := time.Date(year, time.Month(mon), day, ai(m[5]), ai(m[6]), ai(m[7]), 0, time.Local)
	// reject implausible (corrupt match) — too old or in the future
	if int(t.Month()) != mon || t.Day() != day || t.Year() < 2000 || t.After(time.Now().Add(48*time.Hour)) {
		return mtime
	}
	return t.Unix()
}

// photoNoise filters out surveillance/NVR clips (Frigate etc.) and cache art
// that would otherwise flood a "photos" timeline and bury real pictures.
var photoNoiseSeg = map[string]bool{
	"frigate": true, "recordings": true, "recording": true,
	"thumbs": true, "thumbnails": true, ".thumbnails": true, "transcoded": true,
	"cache": true, "image_cache": true, "recheck-cache": true, "encoded-video": true,
	"films": true, "film": true, "movies": true, "movie": true, "series": true, "tv": true,
}

func photoNoise(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if photoNoiseSeg[seg] {
			return true
		}
	}
	return false
}

func handlePhotos(w http.ResponseWriter, r *http.Request) {
	if db == nil {
		writeJSON(w, map[string]any{"photos": []Photo{}})
		return
	}
	q := r.URL.Query()
	source := q.Get("source")   // "" = all sources
	pathPrefix := q.Get("path") // "" = whole source; else only under this subfolder
	if source != "" {
		if _, ok := byName[source]; !ok {
			http.Error(w, "bad source", http.StatusBadRequest)
			return
		}
	}
	// The all-sources timeline is cached (30 s) so paginated scroll doesn't
	// re-scan + re-sort the whole 500k-row index on every page request.
	out := profilePhotos(r)
	snapshot := q.Get("snapshot")
	if snapshot != "" && snapshot != photoManifestToken(out) {
		http.Error(w, "photo snapshot changed", http.StatusConflict)
		return
	}
	if source != "" || pathPrefix != "" {
		scoped := make([]Photo, 0)
		for _, p := range out {
			if source != "" && p.Source != source {
				continue
			}
			if pathPrefix != "" && p.Path != pathPrefix && !strings.HasPrefix(p.Path, pathPrefix+"/") {
				continue
			}
			scoped = append(scoped, p)
		}
		out = scoped
	}
	// totals over the whole library (before the timeline cap)
	total := len(out)
	vids := 0
	for _, p := range out {
		if p.IsVideo {
			vids++
		}
	}
	if cursor := q.Get("after"); cursor != "" {
		last, err := decodePhotoCursor(cursor)
		if err != nil {
			http.Error(w, "invalid photo cursor", http.StatusBadRequest)
			return
		}
		first := sort.Search(len(out), func(i int) bool { return photoBefore(last, out[i]) })
		out = out[first:]
	} else if off, err := strconv.Atoi(q.Get("offset")); err == nil && off > 0 {
		if off < len(out) {
			out = out[off:]
		} else {
			out = out[:0]
		}
	}
	limit := photosCap
	if snapshot != "" {
		limit = photoSnapshotPageLimit
	}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v < limit {
		limit = v
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = encodePhotoCursor(out[len(out)-1])
	}
	if snapshot == "" {
		prewarmThumbs(out[:min(len(out), 96)])
	}
	writeJSON(w, map[string]any{"photos": out, "count": len(out), "total": total, "images": total - vids, "videos": vids, "nextCursor": next})
}

// photoScope restricts the Photos timeline (and image-OCR) to specific folders,
// set via FILEHUB_PHOTO_DIRS="HDD2:Photos,HDD1:Camera" (source:pathPrefix, comma-sep).
// Empty = every disk (old behaviour). Lets the user point Photos at one folder.
type scopeDir struct{ source, prefix string }

var photoScope = func() []scopeDir {
	raw := env("FILEHUB_PHOTO_DIRS", "")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []scopeDir
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		src, pre, _ := strings.Cut(part, ":")
		out = append(out, scopeDir{strings.TrimSpace(src), strings.Trim(strings.TrimSpace(pre), "/")})
	}
	return out
}()

func inPhotoScope(source, path string) bool {
	if len(photoScope) == 0 {
		return true
	}
	for _, d := range photoScope {
		if d.source != source {
			continue
		}
		if d.prefix == "" || path == d.prefix || strings.HasPrefix(path, d.prefix+"/") {
			return true
		}
	}
	return false
}

// file_recents maintains the same indexed files under a source/path primary
// key. A configured photo directory is a small range in that index.
func scopedPhotoIndex() (string, []any) {
	if len(photoScope) == 0 || !recentIndexReady() {
		return "", nil
	}
	var filters []string
	var args []any
	for _, scope := range photoScope {
		if scope.prefix == "" {
			filters = append(filters, "source=?")
			args = append(args, scope.source)
		} else {
			filters = append(filters, "(source=? AND (path=? OR (path>=? AND path<?)))")
			args = append(args, scope.source, scope.prefix, scope.prefix+"/", scope.prefix+"0")
		}
	}
	return "SELECT name,path,source,mtime,size FROM file_recents WHERE (" + strings.Join(filters, " OR ") + ")", args
}

// buildPhotoList scans the index for media (optionally within source/pathPrefix),
// resolves each item's effective capture date, and returns it newest-first.
func buildPhotoList(source, pathPrefix string) []Photo {
	out := []Photo{}
	if db == nil {
		return out
	}
	query, args := scopedPhotoIndex()
	if query == "" {
		extensions := make([]string, 0, len(thumbExt)+len(videoExt))
		for ext := range thumbExt {
			extensions = append(extensions, ext)
		}
		for ext := range videoExt {
			extensions = append(extensions, ext)
		}
		sort.Strings(extensions)
		query = `SELECT name,path,source,mtime,size FROM files WHERE isdir=0`
		if match := extensionMatch(extensions); match != "" {
			query += ` AND files MATCH ?`
			args = append(args, match)
		}
	}
	if source != "" {
		query += ` AND source=?`
		args = append(args, source)
	}
	taken := photoTakenMap()
	revisions := mediaRevisionMap()
	types := mediaTypeMap()
	rows, err := db.Query(query, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name, path, src string
		var mt, size int64
		if rows.Scan(&name, &path, &src, &mt, &size) != nil {
			continue
		}
		if pathPrefix != "" && path != pathPrefix && !strings.HasPrefix(path, pathPrefix+"/") {
			continue
		}
		if photoNoise(path) || (!isThumbable(name) && !isVideoThumb(name)) {
			continue
		}
		if !inPhotoScope(src, path) {
			continue
		}
		// NOTE: physical dedup (content-verified md5, keeps one copy) handles true
		// duplicates; do NOT collapse by name+size here — 46 real photos share a
		// name+size with different content and must both show.
		date := captureDate(name, mt)
		if t, ok := taken[src+"|"+path]; ok && t > 0 {
			date = t
		}
		out = append(out, Photo{name, path, src, date, size, photoVideo(types, src, path, name), photoRevision(revisions, src, path, size, mt) + mediaTypeRevision(types, src, path, name)})
	}
	sort.Slice(out, func(i, j int) bool { return photoBefore(out[i], out[j]) })
	return out
}

var (
	photoCacheMu   sync.Mutex
	photoCacheList []Photo
	photoCacheAt   int64
)

const photoCacheTTL = 30 // seconds

// cachedAllPhotos returns the full all-sources timeline, rebuilt at most every
// photoCacheTTL seconds (so paginated scroll serves from memory).
func cachedAllPhotos() []Photo {
	photoCacheMu.Lock()
	defer photoCacheMu.Unlock()
	now := time.Now().Unix()
	if photoCacheList != nil && now-photoCacheAt < photoCacheTTL {
		return photoCacheList
	}
	photoCacheList = buildPhotoList("", "")
	photoCacheAt = now
	return photoCacheList
}

// Tie-breakers keep a page stable when many files share the same capture date.
func photoBefore(a, b Photo) bool {
	if a.ModTime != b.ModTime {
		return a.ModTime > b.ModTime
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Path < b.Path
}
func encodePhotoCursor(p Photo) string {
	raw, _ := json.Marshal(struct {
		Time   int64  `json:"t"`
		Source string `json:"s"`
		Path   string `json:"p"`
	}{p.ModTime, p.Source, p.Path})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodePhotoCursor(raw string) (Photo, error) {
	if len(raw) > 8192 {
		return Photo{}, errors.New("cursor too large")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Photo{}, err
	}
	var v struct {
		Time   int64  `json:"t"`
		Source string `json:"s"`
		Path   string `json:"p"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return Photo{}, err
	}
	if v.Source == "" || v.Path == "" {
		return Photo{}, errors.New("incomplete cursor")
	}
	return Photo{ModTime: v.Time, Source: v.Source, Path: v.Path}, nil
}

// Build the per-profile view before totals, cursors or Android delta calculation.
func profilePhotos(r *http.Request) []Photo {
	all := cachedAllPhotos()
	access := requestFolders(r.Context())
	if access == nil {
		return all
	}
	out := []Photo{}
	for _, photo := range all {
		if access.canRead(photo.Source, photo.Path) {
			out = append(out, photo)
		}
	}
	return out
}
