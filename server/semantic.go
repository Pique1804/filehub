// Semantic ("search by meaning") photo search via CLIP embeddings.
//
// The Go server deliberately carries NO ML runtime. A sidecar (Python + CLIP,
// running on the user's GPU box — see clip-sidecar/) does all the heavy lifting:
//   - pulls the photo list from /api/embed/todo,
//   - encodes each image to a vector, POSTs them to /api/embed/push,
//   - exposes /embed_text so the server can turn a text query into a vector.
//
// The server just stores the image vectors and does the (cheap) cosine search in
// Go over ~9k×512 floats — no GPU needed for querying. Vectors are L2-normalized
// on store, so cosine similarity == dot product.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const embModel = "ViT-B-32" // must match the sidecar

func initEmbed() error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS photo_embed(
		source TEXT, path TEXT, model TEXT, dim INTEGER, vec BLOB, ts INTEGER,
		file_stamp TEXT NOT NULL DEFAULT '', PRIMARY KEY(source,path))`); err != nil {
		return err
	}
	var stamp int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('photo_embed') WHERE name='file_stamp'`).Scan(&stamp); err != nil {
		return err
	}
	if stamp == 0 {
		_, err := db.Exec(`ALTER TABLE photo_embed ADD COLUMN file_stamp TEXT NOT NULL DEFAULT ''`)
		return err
	}
	return nil
}

// ---- vector packing / cache ------------------------------------------------

func packVec(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}
func unpackVec(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
func normalize(v []float32) {
	var s float64
	for _, f := range v {
		s += float64(f) * float64(f)
	}
	if s == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(s))
	for i := range v {
		v[i] *= inv
	}
}

type embRow struct {
	source, path string
	vec          []float32
	stamp        string
	indexedAt    int64
}

var (
	embMu    sync.RWMutex
	embCache []embRow
	embDirty = true // reload from DB on next search
)

func loadEmbCache() []embRow {
	embMu.RLock()
	if !embDirty && embCache != nil {
		c := embCache
		embMu.RUnlock()
		return c
	}
	embMu.RUnlock()

	embMu.Lock()
	defer embMu.Unlock()
	if !embDirty && embCache != nil {
		return embCache
	}
	rows, err := db.Query(`SELECT source,path,vec,file_stamp,ts FROM photo_embed WHERE model=?`, embModel)
	if err != nil {
		return embCache
	}
	defer rows.Close()
	out := []embRow{}
	for rows.Next() {
		var entry embRow
		var b []byte
		if rows.Scan(&entry.source, &entry.path, &b, &entry.stamp, &entry.indexedAt) == nil {
			entry.vec = unpackVec(b)
			out = append(out, entry)
		}
	}
	embCache = out
	embDirty = false
	return out
}

func visualFile(source, path string) (string, os.FileInfo, bool) {
	if photoNoise(path) || !inPhotoScope(source, path) || !isThumbable(path) {
		return "", nil, false
	}
	full, ok := realProfilePath(source, path)
	if !ok {
		return "", nil, false
	}
	info, err := os.Stat(full)
	return full, info, err == nil && info.Mode().IsRegular()
}

func embeddingCurrent(entry embRow, info os.FileInfo) bool {
	if entry.stamp != "" {
		return entry.stamp == photoFileStamp(info)
	}
	// Older GPU clients recorded only the time the vector was stored.
	if info.ModTime().Unix() > entry.indexedAt {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return !ok || stat.Ctim.Sec <= entry.indexedAt
}

func clipBase() string {
	u, err := url.Parse(env("FILEHUB_CLIP_SIDECAR", ""))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return ""
	}
	return strings.TrimRight(u.String(), "/")
}

var errClipBusy = errors.New("local visual search model is busy")

var clipClient = &http.Client{
	Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func clipVector(ctx context.Context, base, target string, image []byte) ([]float32, error) {
	if base == "" {
		return nil, errors.New("local visual search model is not configured")
	}
	method := http.MethodGet
	if image != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, base+target, bytes.NewReader(image))
	if err != nil {
		return nil, err
	}
	if image != nil {
		req.Header.Set("Content-Type", "image/jpeg")
	}
	resp, err := clipClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, errClipBusy
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("local visual search model unavailable")
	}
	var out struct {
		Vec []float32 `json:"vec"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, err
	}
	if !validEmbedding(out.Vec) {
		return nil, errors.New("invalid visual search vector")
	}
	normalize(out.Vec)
	return out.Vec, nil
}

func validEmbedding(vec []float32) bool {
	if len(vec) != 512 {
		return false
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	return norm > 0 && !math.IsInf(norm, 0) && !math.IsNaN(norm)
}

func queryVector(ctx context.Context, q string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	base, target := clipBase(), "/embed_text?q="+url.QueryEscape(q)
	for _, delay := range []time.Duration{150 * time.Millisecond, 350 * time.Millisecond, 750 * time.Millisecond} {
		vec, err := clipVector(ctx, base, target, nil)
		if !errors.Is(err, errClipBusy) {
			return vec, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return clipVector(ctx, base, target, nil)
}

type visualQueryEntry struct {
	vector  []float32
	expires time.Time
}

func visualQueryDescription(q string) string {
	if strings.IndexFunc(q, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) == -1 {
		return "an image showing " + q
	}
	return q
}

func (s *visualSearch) queryEmbedding(ctx context.Context, q string) ([]float32, error) {
	q = visualQueryDescription(q)
	key := sha256.Sum256([]byte(q))
	s.queryMu.Lock()
	cached := s.queries[key]
	s.queryMu.Unlock()
	if time.Now().Before(cached.expires) {
		return cached.vector, ctx.Err()
	}
	vec, err := queryVector(ctx, q)
	if err != nil {
		return nil, err
	}
	s.queryMu.Lock()
	if s.queries == nil || len(s.queries) >= 64 {
		s.queries = make(map[[32]byte]visualQueryEntry)
	}
	s.queries[key] = visualQueryEntry{vec, time.Now().Add(2 * time.Minute)}
	s.queryMu.Unlock()
	return vec, nil
}

type visualCursor struct {
	Score  float32 `json:"r"`
	Source string  `json:"s"`
	Path   string  `json:"p"`
}

func readVisualCursor(raw string) (visualCursor, bool) {
	var cursor visualCursor
	if len(raw) > 8192 {
		return cursor, false
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &cursor) != nil {
		return cursor, false
	}
	return cursor, cursor.Source != "" && cursor.Path != "" && cursor.Score >= -1.01 && cursor.Score <= 1.01
}

func visualBefore(a, b visualCursor) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Path < b.Path
}

func (s *visualSearch) search(w http.ResponseWriter, r *http.Request) {
	run := s.operation(r.Context())
	defer run.cancel()
	w.Header().Set("Cache-Control", "private, no-store")
	if run.ctx == nil {
		http.Error(w, "visual search disabled", http.StatusServiceUnavailable)
		return
	}
	params := r.URL.Query()
	q := strings.TrimSpace(params.Get("q"))
	if len([]rune(q)) < 2 || len(q) > 2000 {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	if params.Get("mode") == "auto" {
		_, intent := parseSearchIntent(q, "auto", "image", "video")
		if intent != nil || (strings.HasPrefix(q, `"`) && strings.HasSuffix(q, `"`)) {
			writeJSON(w, photoSearchResponse{Photos: []Photo{}, Intent: intent})
			return
		}
	}
	var after *visualCursor
	if raw := params.Get("after"); raw != "" {
		cursor, ok := readVisualCursor(raw)
		if !ok {
			http.Error(w, "invalid visual cursor", http.StatusBadRequest)
			return
		}
		after = &cursor
	}
	k := 60
	if v, err := strconv.Atoi(params.Get("k")); err == nil && v > 0 && v <= 500 {
		k = v
	}
	qv, err := s.queryEmbedding(run.ctx, q)
	s.recordHealth(run.ctx, err == nil)
	if err != nil || run.ctx.Err() != nil {
		http.Error(w, "local visual search model unavailable", http.StatusServiceUnavailable)
		return
	}
	// A query may have been waiting on the model while folder access changed.
	account, ok := requestProfile(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	profile, err := loadProfile(account.username)
	if err != nil || profile.version != account.version {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out, next, err := rankVisualPhotos(withProfile(r.WithContext(run.ctx), profile), qv, k, after)
	if err != nil {
		http.Error(w, "visual search unavailable", http.StatusServiceUnavailable)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if run.ctx.Err() != nil {
		http.Error(w, "visual search interrupted", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, photoSearchResponse{Photos: out, Count: len(out), NextCursor: next})
}

func rankVisualPhotos(r *http.Request, qv []float32, limit int, after *visualCursor) ([]Photo, string, error) {
	key, cacheable := visualRankingCacheKey(r, qv)
	var hits []visualHit
	var cached bool
	if cacheable {
		hits, cached = cachedVisualRanking(key)
	}
	if !cached {
		var err error
		hits, err = buildVisualRanking(r, qv)
		if err != nil {
			return nil, "", err
		}
		if cacheable {
			cacheVisualRanking(key, hits)
		}
	}
	if after != nil {
		first := sort.Search(len(hits), func(i int) bool { return visualBefore(*after, hits[i].cursor) })
		hits = hits[first:]
	}
	out := make([]Photo, 0, min(limit, len(hits)))
	var last visualCursor
	access := requestFolders(r.Context())
	for _, h := range hits {
		if err := r.Context().Err(); err != nil {
			return nil, "", err
		}
		if !access.canRead(h.entry.source, h.entry.path) {
			continue
		}
		if _, info, ok := visualFile(h.entry.source, h.entry.path); ok && embeddingCurrent(h.entry, info) {
			if len(out) == limit {
				raw, _ := json.Marshal(last)
				return out, base64.RawURLEncoding.EncodeToString(raw), nil
			}
			out = append(out, h.photo)
			last = h.cursor
		}
	}
	return out, "", nil
}

func buildVisualRanking(r *http.Request, qv []float32) ([]visualHit, error) {
	params := r.URL.Query()
	source, prefix := params.Get("source"), params.Get("path")
	var album map[photoSearchKey]bool
	if id := params.Get("album"); id != "" {
		var err error
		album, err = photoSearchIDs(r.Context(), `SELECT source,path FROM album_photos WHERE album_id=?`, id, nil)
		if err != nil {
			return nil, err
		}
	}
	visible := make(map[photoSearchKey]Photo)
	for _, p := range profilePhotos(r) {
		if !p.IsVideo {
			visible[photoSearchKey{p.Source, p.Path}] = p
		}
	}
	var hits []visualHit
	var samples int
	var sum, sumSquares float64
	for i, entry := range loadEmbCache() {
		if i%1024 == 0 && r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		key := photoSearchKey{entry.source, entry.path}
		p, ok := visible[key]
		if !ok || len(qv) != len(entry.vec) {
			continue
		}
		var score float32
		for i, v := range entry.vec {
			score += v * qv[i]
		}
		similarity := float64(score)
		if math.IsNaN(similarity) || math.IsInf(similarity, 0) {
			continue
		}
		samples++
		sum += similarity
		sumSquares += similarity * similarity
		if (source == "" || p.Source == source) && pathWithin(p.Path, prefix) && (album == nil || album[key]) {
			cursor := visualCursor{score, p.Source, p.Path}
			entry.vec = nil
			hits = append(hits, visualHit{entry, p, cursor})
		}
	}
	// CLIP assigns even unrelated photos a positive similarity. Use the
	// profile's visible library as a background for this query. An album or
	// a later page must use the same threshold as the first page.
	cutoff := float32(0.19)
	if samples >= 32 {
		mean := sum / float64(samples)
		variance := max(0, sumSquares/float64(samples)-mean*mean)
		cutoff = float32(max(0.19, min(0.30, mean+3.35*math.Sqrt(variance))))
	}
	hits = slices.DeleteFunc(hits, func(h visualHit) bool {
		return h.cursor.Score < cutoff
	})
	sort.Slice(hits, func(i, j int) bool { return visualBefore(hits[i].cursor, hits[j].cursor) })
	return hits, nil
}
