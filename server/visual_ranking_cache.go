package main

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type visualHit struct {
	entry  embRow
	photo  Photo
	cursor visualCursor
}

type visualRankingKey struct {
	username, version, policy, source, prefix string
	query                                     [sha256.Size]byte
	generation                                uint64
}

type visualRanking struct {
	hits    []visualHit
	expires time.Time
}

// Keep a few searches for short bursts of pagination. Album membership can
// change independently, so album searches use a fresh ranking.
var visualRankingGeneration atomic.Uint64
var visualRankings = struct {
	sync.Mutex
	entries map[visualRankingKey]visualRanking
}{entries: make(map[visualRankingKey]visualRanking)}

func visualRankingCacheKey(r *http.Request, qv []float32) (visualRankingKey, bool) {
	account, ok := requestProfile(r)
	params := r.URL.Query()
	if !ok || params.Get("album") != "" {
		return visualRankingKey{}, false
	}
	policy, _ := json.Marshal(account.folders)
	return visualRankingKey{
		username: account.username, version: account.version, policy: string(policy),
		source: params.Get("source"), prefix: params.Get("path"),
		query: sha256.Sum256(packVec(qv)), generation: visualRankingGeneration.Load(),
	}, true
}

func cachedVisualRanking(key visualRankingKey) ([]visualHit, bool) {
	visualRankings.Lock()
	defer visualRankings.Unlock()
	entry, found := visualRankings.entries[key]
	return entry.hits, found && time.Now().Before(entry.expires)
}

func cacheVisualRanking(key visualRankingKey, hits []visualHit) {
	// Unusually broad searches remain supported without retaining large lists.
	if len(hits) > 20000 || key.generation != visualRankingGeneration.Load() {
		return
	}
	visualRankings.Lock()
	defer visualRankings.Unlock()
	if len(visualRankings.entries) >= 4 {
		clear(visualRankings.entries)
	}
	visualRankings.entries[key] = visualRanking{hits, time.Now().Add(20 * time.Second)}
}
