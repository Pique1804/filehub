package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type visualSearch struct {
	mu        sync.RWMutex
	base      string
	run       context.Context
	stop      context.CancelFunc
	wake      chan struct{}
	available bool
	checked   time.Time
	indexing  bool
	probed    sync.Mutex
	queryMu   sync.Mutex
	queries   map[[32]byte]visualQueryEntry
}

type visualStatus struct {
	Enabled    bool `json:"enabled"`
	Configured bool `json:"configured"`
	Available  bool `json:"available"`
	Indexed    int  `json:"indexed"`
	Total      int  `json:"total"`
	Indexing   bool `json:"indexing"`
}

func newVisualSearch() (*visualSearch, error) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS visual_search_settings (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL)`); err != nil {
		return nil, err
	}
	s := &visualSearch{base: clipBase(), wake: make(chan struct{}, 1)}
	var enabled bool
	err := db.QueryRow(`SELECT enabled FROM visual_search_settings WHERE id=1`).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		enabled = s.base != "" && env("FILEHUB_CLIP_DEFAULT_ENABLED", "1") != "0"
	} else if err != nil {
		return nil, err
	}
	if enabled && s.base != "" {
		s.run, s.stop = context.WithCancel(context.Background())
	}
	return s, nil
}

func registerVisualSearch(mux *http.ServeMux) error {
	if err := initEmbed(); err != nil {
		return err
	}
	s, err := newVisualSearch()
	if err != nil {
		return err
	}
	mux.HandleFunc("GET /api/photos/visual-search", requireBrowse(s.status(false)))
	mux.HandleFunc("GET /api/admin/photos/visual-search", requireAdmin(s.status(true)))
	mux.HandleFunc("POST /api/admin/photos/visual-search", requireAdminMutation(s.setEnabled))
	mux.HandleFunc("GET /api/embed/status", requireAdmin(s.legacyStatus))
	mux.HandleFunc("GET /api/embed/todo", requireAdmin(s.todo))
	mux.HandleFunc("POST /api/embed/push", requireAdminMutation(s.push))
	mux.HandleFunc("/api/search/semantic", requireBrowse(s.search))
	go s.worker()
	return nil
}

type visualOperation struct {
	ctx    context.Context
	cancel func()
}

func (s *visualSearch) operation(parent context.Context) visualOperation {
	s.mu.RLock()
	run := s.run
	s.mu.RUnlock()
	if run == nil {
		return visualOperation{cancel: func() {}}
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(run, cancel)
	if run.Err() != nil {
		cancel()
	}
	return visualOperation{ctx, func() { stop(); cancel() }}
}

func (s *visualSearch) setEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.Enabled == nil {
		http.Error(w, "invalid visual search setting", http.StatusBadRequest)
		return
	}
	if *body.Enabled && s.base == "" {
		http.Error(w, "configure a local CLIP endpoint first", http.StatusConflict)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := db.ExecContext(r.Context(), `INSERT INTO visual_search_settings(id,enabled) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled`, *body.Enabled); err != nil {
		http.Error(w, "could not save visual search setting", http.StatusServiceUnavailable)
		return
	}
	if *body.Enabled && s.run == nil {
		s.run, s.stop = context.WithCancel(context.Background())
		select {
		case s.wake <- struct{}{}:
		default:
		}
	} else if !*body.Enabled && s.run != nil {
		s.stop()
		s.run, s.stop = nil, nil
		s.available, s.indexing = false, false
		s.checked = time.Time{}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *visualSearch) recordHealth(ctx context.Context, ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() == nil && s.run != nil {
		s.available = ready
		s.checked = time.Now()
	}
}

func (s *visualSearch) probe(parent context.Context) {
	if !s.probed.TryLock() {
		return
	}
	defer s.probed.Unlock()
	s.mu.RLock()
	run, checked := s.run, s.checked
	s.mu.RUnlock()
	if run == nil || time.Since(checked) < 10*time.Second {
		return
	}
	op := s.operation(parent)
	defer op.cancel()
	if op.ctx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(op.ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/health", nil)
	if err != nil {
		return
	}
	resp, err := clipClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	s.recordHealth(op.ctx, err == nil && resp.StatusCode == http.StatusOK)
}

func (s *visualSearch) status(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.probe(r.Context())
		s.mu.RLock()
		out := visualStatus{Enabled: s.run != nil, Configured: s.base != "", Available: s.available}
		if admin {
			out.Indexing = s.indexing
		}
		s.mu.RUnlock()
		if admin && out.Enabled {
			out.Indexed, out.Total = visualCounts(r.Context())
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, out)
	}
}

func (s *visualSearch) legacyStatus(w http.ResponseWriter, r *http.Request) {
	indexed, total := visualCounts(r.Context())
	writeJSON(w, map[string]any{"embedded": indexed, "total": total, "model": embModel})
}

func embeddedPhotos() map[photoSearchKey]embRow {
	cache := loadEmbCache()
	out := make(map[photoSearchKey]embRow, len(cache))
	for _, entry := range cache {
		out[photoSearchKey{entry.source, entry.path}] = entry
	}
	return out
}

func visualCounts(ctx context.Context) (indexed, total int) {
	entries := embeddedPhotos()
	for _, p := range cachedAllPhotos() {
		if ctx.Err() != nil {
			break
		}
		if p.IsVideo {
			continue
		}
		if _, info, ok := visualFile(p.Source, p.Path); ok {
			total++
			if entry, found := entries[photoSearchKey{p.Source, p.Path}]; found && embeddingCurrent(entry, info) {
				indexed++
			}
		}
	}
	return indexed, total
}

func (s *visualSearch) todo(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Source   string `json:"source"`
		Path     string `json:"path"`
		Revision string `json:"revision"`
	}
	items := []item{}
	run := s.operation(r.Context())
	defer run.cancel()
	limit := 500
	if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 && n <= 5000 {
		limit = n
	}
	if run.ctx != nil {
		entries := embeddedPhotos()
		for _, p := range cachedAllPhotos() {
			if run.ctx.Err() != nil || len(items) >= limit {
				break
			}
			if p.IsVideo {
				continue
			}
			if _, info, ok := visualFile(p.Source, p.Path); ok {
				entry, found := entries[photoSearchKey{p.Source, p.Path}]
				if !found || !embeddingCurrent(entry, info) {
					items = append(items, item{p.Source, p.Path, photoFileStamp(info)})
				}
			}
		}
		if run.ctx.Err() != nil {
			items = items[:0]
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, map[string]any{"items": items, "model": embModel})
}

func (s *visualSearch) push(w http.ResponseWriter, r *http.Request) {
	run := s.operation(r.Context())
	defer run.cancel()
	if run.ctx == nil {
		http.Error(w, "visual search disabled", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Model string `json:"model"`
		Items []struct {
			Source   string    `json:"source"`
			Path     string    `json:"path"`
			Revision string    `json:"revision"`
			Vec      []float32 `json:"vec"`
		} `json:"items"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF ||
		(body.Model != "" && body.Model != embModel) || len(body.Items) > 500 {
		http.Error(w, "invalid embedding batch", http.StatusBadRequest)
		return
	}
	stored := 0
	for _, item := range body.Items {
		if run.ctx.Err() != nil {
			break
		}
		if s.store(run.ctx, item.Source, item.Path, item.Revision, item.Vec) {
			stored++
		}
	}
	writeJSON(w, map[string]any{"ok": true, "stored": stored})
}

func (s *visualSearch) store(ctx context.Context, source, path, stamp string, vec []float32) bool {
	if stamp == "" || !validEmbedding(vec) || ctx.Err() != nil {
		return false
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	if _, info, ok := visualFile(source, path); !ok || photoFileStamp(info) != stamp {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ctx.Err() != nil || s.run == nil {
		return false
	}
	normalize(vec)
	_, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO photo_embed(source,path,model,dim,vec,ts,file_stamp) VALUES(?,?,?,?,?,?,?)`,
		source, path, embModel, len(vec), packVec(vec), time.Now().Unix(), stamp)
	if err != nil {
		return false
	}
	embMu.Lock()
	embDirty = true
	visualRankingGeneration.Add(1)
	embMu.Unlock()
	return true
}
