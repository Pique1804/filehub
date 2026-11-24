// Small additive features over the existing index + a tiny favorites table:
// favorites (star), recents (newest files), and "On This Day" photo memories.
package main

import (
	"math"
	"net/http"
	"sync"
	"time"
)

func initFavorites() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS favorites(
		source TEXT, path TEXT, ts INTEGER, PRIMARY KEY(source,path))`)
	return err
}

func handleFavToggle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source, path := q.Get("source"), q.Get("path")
	if _, ok := byName[source]; !ok || path == "" {
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, "Не удалось сохранить избранное", 503)
		return
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow(`SELECT count(*) FROM favorites WHERE source=? AND path=?`, source, path).Scan(&n); err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	fav := n == 0
	if fav {
		_, err = tx.Exec(`INSERT INTO favorites(source,path,ts) VALUES(?,?,?)`, source, path, time.Now().Unix())
	} else {
		_, err = tx.Exec(`DELETE FROM favorites WHERE source=? AND path=?`, source, path)
	}
	if err != nil {
		http.Error(w, "Не удалось сохранить избранное", 503)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, "Не удалось сохранить избранное", 503)
		return
	}

	writeJSON(w, map[string]any{"ok": true, "favorite": fav})
}

func handleFavList(w http.ResponseWriter, r *http.Request) {
	out := []map[string]string{}
	if db != nil {
		if rows, err := db.Query(`SELECT source,path FROM favorites ORDER BY ts DESC`); err == nil {
			defer rows.Close()
			for rows.Next() {
				var s, p string
				if rows.Scan(&s, &p) == nil {
					out = append(out, map[string]string{"source": s, "path": p})
				}
			}
		}
	}
	writeJSON(w, map[string]any{"items": out})
}

// handleOnThisDay: media whose capture date is today's month+day in a prior year.
// Reuses the cached photo timeline (already media-filtered, capture-dated and
// sorted newest-first) instead of re-scanning the whole ~500k-row files table and
// rebuilding photoTakenMap on every Photos-screen open.
func handleOnThisDay(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	out := []Photo{}
	for _, p := range profilePhotos(r) { // pre-sorted desc → out stays desc
		t := time.Unix(p.ModTime, 0)
		if t.Month() == now.Month() && t.Day() == now.Day() && t.Year() < now.Year() {
			out = append(out, p)
		}
	}
	if len(out) > 200 {
		out = out[:200]
	}
	prewarmThumbs(out[:min(len(out), 12)])
	writeJSON(w, map[string]any{"photos": out, "count": len(out)})
}

// handlePlaces: every photo that has GPS, for the map view.
func handlePlaces(w http.ResponseWriter, r *http.Request) {
	type place struct {
		Source   string  `json:"source"`
		Path     string  `json:"path"`
		Lat      float64 `json:"lat"`
		Lon      float64 `json:"lon"`
		ModTime  int64   `json:"modTime"`
		Name     string  `json:"name"`
		IsVideo  bool    `json:"isVideo"`
		Revision string  `json:"revision"`
		Size     int64   `json:"size"`
	}
	out := []place{}
	if db != nil {
		byID := make(map[string]Photo)
		for _, p := range profilePhotos(r) {
			byID[p.Source+"|"+p.Path] = p
		}
		if rows, err := db.Query(`SELECT source,path,lat,lon FROM photo_meta WHERE lat<>0 OR lon<>0`); err == nil {
			defer rows.Close()
			for rows.Next() {
				var s, p string
				var la, lo float64
				if rows.Scan(&s, &p, &la, &lo) == nil {
					if photo, ok := byID[s+"|"+p]; ok && !math.IsNaN(la) && !math.IsNaN(lo) && !math.IsInf(la, 0) && !math.IsInf(lo, 0) && math.Abs(la) <= 90 && math.Abs(lo) <= 180 {
						out = append(out, place{s, p, la, lo, photo.ModTime, photo.Name, photo.IsVideo, photo.Revision, photo.Size})
					}
				}
			}
		}
	}
	writeJSON(w, map[string]any{"places": out})
}

type recentFile struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	ModTime int64  `json:"modTime"`
	Size    int64  `json:"size"`
	Owner   string `json:"owner,omitempty"`
}

// Keep the fallback query cached while the derived index is initializing.
var (
	recentsMu    sync.Mutex
	recentsCache []recentFile
	recentsAt    int64
)

const recentsTTL = 60 // seconds

// handleRecents: newest non-noise files (any type) by mtime.
func handleRecents(w http.ResponseWriter, r *http.Request) {
	access := requestFolders(r.Context())
	indexed := recentIndexReady()
	cache := !indexed && access == nil
	if cache {
		recentsMu.Lock()
		cached, fresh := recentsCache, time.Now().Unix()-recentsAt < recentsTTL
		recentsMu.Unlock()
		if cached != nil && fresh {
			writeRecentFileOwners(w, r, append([]recentFile{}, cached...))
			return
		}
	}

	out := []recentFile{}
	if db != nil {
		query := `SELECT name,path,source,mtime,size FROM files WHERE isdir=0`
		order := ` ORDER BY mtime DESC LIMIT 400`
		var scope string
		var args []any
		if indexed {
			query = `SELECT name,path,source,mtime,size FROM file_recents WHERE 1=1`
			scope, args = access.indexedSQLCondition("source", "path")
			if access != nil {
				// Apply the source/path ranges before sorting. Otherwise SQLite
				// can walk the global mtime index looking for a few old matches.
				order = ` ORDER BY +mtime DESC LIMIT 400`
			}
		} else {
			scope, args = access.sqlCondition("source", "path")
		}
		if rows, err := db.QueryContext(r.Context(), query+scope+order, args...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var name, path, src string
				var mt, size int64
				if rows.Scan(&name, &path, &src, &mt, &size) != nil {
					continue
				}
				if photoNoise(path) {
					continue
				}
				out = append(out, recentFile{Name: name, Path: path, Source: src, ModTime: mt, Size: size})
				if len(out) >= 100 {
					break
				}
			}
			rows.Close()
		}
	}
	if cache {
		recentsMu.Lock()
		recentsCache = append([]recentFile{}, out...)
		recentsAt = time.Now().Unix()
		recentsMu.Unlock()
	}
	writeRecentFileOwners(w, r, out)
}

func writeRecentFileOwners(w http.ResponseWriter, r *http.Request, out []recentFile) {
	keys := make([]ownerPath, 0, len(out))
	for _, item := range out {
		keys = append(keys, ownerPath{item.Source, item.Path})
	}
	owners := fileOwnerLabels(r.Context(), keys, false)
	for i := range out {
		out[i].Owner = owners[ownerPath{out[i].Source, out[i].Path}]
	}
	writeJSON(w, map[string]any{"items": out})
}
