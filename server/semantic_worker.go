package main

import (
	"context"
	"os"
	"time"
)

func (s *visualSearch) worker() {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
		case <-s.wake:
		}
		s.mu.RLock()
		ctx := s.run
		s.mu.RUnlock()
		if ctx != nil {
			s.scan(ctx)
		}
		timer.Reset(2 * time.Minute)
	}
}

func (s *visualSearch) scan(ctx context.Context) {
	entries := embeddedPhotos()
	defer func() {
		s.mu.Lock()
		if ctx == s.run {
			s.indexing = false
		}
		s.mu.Unlock()
	}()
	for _, p := range cachedAllPhotos() {
		if ctx.Err() != nil {
			break
		}
		if p.IsVideo {
			continue
		}
		full, info, ok := visualFile(p.Source, p.Path)
		if !ok {
			continue
		}
		if entry, found := entries[photoSearchKey{p.Source, p.Path}]; found && embeddingCurrent(entry, info) {
			continue
		}
		s.mu.Lock()
		if ctx == s.run {
			s.indexing = true
		}
		s.mu.Unlock()
		data, err := buildThumb(ctx, full, info, 512, func() (*os.File, error) {
			return openArchiveFile(byName[p.Source], p.Path)
		})
		if err != nil {
			continue
		}
		req, cancel := context.WithTimeout(ctx, 30*time.Second)
		vec, err := clipVector(req, s.base, "/embed_image", data)
		cancel()
		if err != nil {
			// Some legacy GPU servers only expose text; their own indexer
			// can still fill the index via the authenticated batch API.
			break
		}
		s.store(ctx, p.Source, p.Path, photoFileStamp(info), vec)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
