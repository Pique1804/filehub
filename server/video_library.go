package main

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type videoFolder struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
}

type libraryVideo struct {
	Source   string  `json:"source"`
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Season   int     `json:"season"`
	Episode  int     `json:"episode"`
	Position float64 `json:"position"`
	Watched  int64   `json:"watched"`
}

type videoTitle struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Name           string         `json:"name"`
	Cover          string         `json:"cover,omitempty"`
	Poster         string         `json:"poster,omitempty"`
	Year           int            `json:"year,omitempty"`
	Description    string         `json:"description,omitempty"`
	Genres         []string       `json:"genres,omitempty"`
	MetadataLink   string         `json:"metadataLink,omitempty"`
	MetadataSource string         `json:"metadataSource,omitempty"`
	TMDBMatchID    int            `json:"tmdbMatchId,omitempty"`
	TMDBNeedsMatch bool           `json:"tmdbNeedsMatch,omitempty"`
	TMDBQuery      string         `json:"tmdbQuery,omitempty"`
	TMDBQueryYear  int            `json:"tmdbQueryYear,omitempty"`
	Videos         []libraryVideo `json:"videos"`
}

type videoPosition struct {
	position float64
	watched  int64
}

var videoScans = make(chan struct{}, 2)
var videoEpisodePattern = regexp.MustCompile(`(?i)s([0-9]{1,3})[ ._-]*e([0-9]{1,4})|([0-9]{1,3})x([0-9]{1,4})`)
var videoSeasonPattern = regexp.MustCompile(`(?i)^(?:season|сезон|s)[ ._-]*([0-9]{1,3})$`)
var videoEpisodeNumber = regexp.MustCompile(`(?i)^(?:e|episode[ ._-]*|серия[ ._-]*)?([0-9]{1,4})(?:[ ._-]|$)`)

func registerVideoLibrary(mux *http.ServeMux) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS video_folders (
			source TEXT NOT NULL, path TEXT NOT NULL, kind TEXT NOT NULL,
			PRIMARY KEY (source, path)
		);
		CREATE TABLE IF NOT EXISTS video_progress (
			username TEXT NOT NULL, source TEXT NOT NULL, path TEXT NOT NULL,
			position REAL NOT NULL, watched INTEGER NOT NULL,
			PRIMARY KEY (username, source, path)
		);
		CREATE TABLE IF NOT EXISTS video_tmdb_matches (
			title_id TEXT PRIMARY KEY, lookup TEXT NOT NULL, result TEXT NOT NULL,
			updated INTEGER NOT NULL, manual INTEGER NOT NULL DEFAULT 0
		);
		CREATE TRIGGER IF NOT EXISTS cleanup_video_progress AFTER DELETE ON users BEGIN
			DELETE FROM video_progress WHERE username=OLD.username;
		END;`)
	if err != nil {
		return err
	}
	mux.HandleFunc("GET /api/video/library", requireBrowse(handleVideoLibrary))
	mux.HandleFunc("/api/admin/video/folder", requireAdminMutation(handleVideoFolder))
	mux.HandleFunc("GET /api/admin/video/tmdb/search", requireAdmin(handleTMDBSearch))
	mux.HandleFunc("POST /api/admin/video/tmdb/match", requireAdminMutation(handleTMDBMatch))
	mux.HandleFunc("GET /api/admin/video/tmdb/settings", requireAdmin(handleTMDBSettings))
	mux.HandleFunc("POST /api/admin/video/tmdb/settings", requireAdminMutation(handleTMDBSettingsSave))
	mux.HandleFunc("/api/video/progress", requireBrowseMutation(handleVideoProgress))
	mux.HandleFunc("GET /api/video/compatible", requireReadablePath(handleVideoCompatible))
	mux.HandleFunc("GET /api/video/hls/index.m3u8", requireReadablePath(handleVideoHLS))
	mux.HandleFunc("GET /api/video/hls/segment.ts", requireReadablePath(handleVideoHLS))
	return nil
}

func handleVideoFolder(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	source := r.PostFormValue("source")
	path := r.PostFormValue("path")
	kind := r.PostFormValue("kind")
	if kind == "remove" {
		if _, err := db.ExecContext(r.Context(), "DELETE FROM video_folders WHERE source=? AND path=?", source, path); err != nil {
			http.Error(w, "could not remove folder", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if kind != "movies" && kind != "series" {
		http.Error(w, "invalid library type", http.StatusBadRequest)
		return
	}
	if path != "" && (!fs.ValidPath(path) || path == "." || strings.Contains(path, "\\")) {
		http.Error(w, "invalid folder", http.StatusBadRequest)
		return
	}
	full, ok := safePath(source, path)
	if !ok {
		http.Error(w, "invalid folder", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(full); err != nil || !info.IsDir() {
		http.Error(w, "folder unavailable", http.StatusNotFound)
		return
	}
	result, err := db.ExecContext(r.Context(), `
		INSERT INTO video_folders(source,path,kind)
		SELECT ?,?,? WHERE EXISTS(SELECT 1 FROM video_folders WHERE source=? AND path=?)
			OR (SELECT count(*) FROM video_folders)<32
		ON CONFLICT(source,path) DO UPDATE SET kind=excluded.kind`, source, path, kind, source, path)
	if err != nil {
		http.Error(w, "could not save folder", http.StatusInternalServerError)
		return
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		http.Error(w, "library folder limit reached", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func playbackPosition(value string) (float64, bool) {
	if value == "" {
		return 0, true
	}
	n, err := strconv.ParseFloat(value, 64)
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 86400
}

func handleVideoProgress(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	user, _, _ := currentUser(r)
	source, path := r.PostFormValue("source"), r.PostFormValue("path")
	position, ok := playbackPosition(r.PostFormValue("position"))
	full, found := requestFolders(r.Context()).readPath(source, path)
	if !ok || !found || !isVideoThumb(full) {
		http.Error(w, "invalid video or position", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if _, err := db.ExecContext(r.Context(), `
		INSERT INTO video_progress(username,source,path,position,watched)
		SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM users WHERE username=?)
		ON CONFLICT(username,source,path) DO UPDATE SET position=excluded.position, watched=excluded.watched`,
		user, source, path, position, time.Now().Unix(), user); err != nil {
		http.Error(w, "could not save position", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readVideoFolders(ctx context.Context) ([]videoFolder, error) {
	rows, err := db.QueryContext(ctx, "SELECT source,path,kind FROM video_folders ORDER BY source,path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	folders := []videoFolder{}
	for rows.Next() {
		var folder videoFolder
		if err := rows.Scan(&folder.Source, &folder.Path, &folder.Kind); err != nil {
			return nil, err
		}
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

func readVideoPositions(ctx context.Context, user string) (map[[2]string]videoPosition, error) {
	rows, err := db.QueryContext(ctx, `SELECT source,path,position,watched FROM video_progress
		WHERE username=? ORDER BY watched DESC LIMIT 5000`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	positions := make(map[[2]string]videoPosition)
	for rows.Next() {
		var key [2]string
		var position videoPosition
		if err := rows.Scan(&key[0], &key[1], &position.position, &position.watched); err != nil {
			return nil, err
		}
		positions[key] = position
	}
	return positions, rows.Err()
}

func handleVideoLibrary(w http.ResponseWriter, r *http.Request) {
	select {
	case videoScans <- struct{}{}:
		defer func() { <-videoScans }()
	default:
		http.Error(w, "library busy", http.StatusServiceUnavailable)
		return
	}
	user, role, _ := currentUser(r)
	folders, err := readVideoFolders(r.Context())
	if err != nil {
		http.Error(w, "could not read folders", http.StatusInternalServerError)
		return
	}
	folders = clipVideoFolders(folders, requestFolders(r.Context()))
	positions, err := readVideoPositions(r.Context(), user)
	if err != nil {
		http.Error(w, "could not read progress", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	catalog, partial := scanVideoLibrary(ctx, folders, positions)
	pending := false
	tmdbEnabled := libraryTMDB.credential() != ""
	if tmdbEnabled {
		pending = libraryTMDB.enrich(r.Context(), catalog)
	} else {
		for _, item := range catalog {
			applyOnlineVideoMetadata(item)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, map[string]any{"folders": folders, "titles": catalog, "partial": partial, "canManage": role == "admin",
		"tmdbEnabled": tmdbEnabled, "tmdbPending": pending})
}

func clipVideoFolders(folders []videoFolder, access *folderAccess) []videoFolder {
	if access == nil {
		return folders
	}
	out := []videoFolder{}
	seen := make(map[videoFolder]bool)
	for _, folder := range folders {
		for _, grant := range access.grants() {
			if grant.Source != folder.Source {
				continue
			}
			clipped := folder
			if pathWithin(grant.Path, folder.Path) {
				clipped.Path = grant.Path
			} else if !pathWithin(folder.Path, grant.Path) {
				continue
			}
			if !seen[clipped] && access.visibleEntry(clipped.Source, clipped.Path) {
				seen[clipped] = true
				out = append(out, clipped)
			}
		}
	}
	return out
}

func scanVideoLibrary(ctx context.Context, folders []videoFolder, positions map[[2]string]videoPosition) ([]*videoTitle, bool) {
	groups := make(map[string]*videoTitle)
	seen := make(map[[2]string]bool)
	const maxEntries = 50000
	const maxVideos = 5000
	remaining := maxEntries
	partial := false
	for _, folder := range folders {
		root, ok := safePath(folder.Source, folder.Path)
		info, err := os.Stat(root)
		if !ok || err != nil || !info.IsDir() {
			partial = true
			continue
		}
		err = filepath.WalkDir(root, func(full string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil || remaining == 0 || len(seen) == maxVideos {
				return fs.SkipAll
			}
			remaining--
			if walkErr != nil {
				partial = true
				return nil
			}
			if entry.IsDir() {
				if full != root && skipDir(entry.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || !isVideoThumb(entry.Name()) || strings.HasPrefix(entry.Name(), ".") {
				return nil
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			path := joinLibraryPath(folder.Path, rel)
			key := [2]string{folder.Source, path}
			if seen[key] {
				return nil
			}
			seen[key] = true
			title, group, season, episode := describeLibraryVideo(folder, rel)
			id := strconv.Itoa(len(folder.Source)) + ":" + folder.Source + "/" + folder.Kind + "/" + group
			section := groups[id]
			if section == nil {
				coverFolder := group
				if folder.Kind == "movies" {
					coverFolder = filepath.ToSlash(filepath.Dir(path))
				}
				cover := libraryCover(folder.Source, coverFolder, path, folder.Kind, requestFolders(ctx))
				section = &videoTitle{ID: id, Kind: folder.Kind, Name: title, Cover: cover}
				applyLocalVideoMetadata(section, folder.Source, group, path, requestFolders(ctx))
				groups[id] = section
			}
			position := positions[key]
			section.Videos = append(section.Videos, libraryVideo{
				Source: folder.Source, Path: path, Name: libraryVideoLabel(entry.Name(), folder.Kind),
				Season: season, Episode: episode, Position: position.position, Watched: position.watched,
			})
			return nil
		})
		partial = partial || err != nil
		if ctx.Err() != nil || remaining == 0 || len(seen) == maxVideos {
			partial = true
			break
		}
	}
	catalog := make([]*videoTitle, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.Videos, func(i, j int) bool {
			a, b := group.Videos[i], group.Videos[j]
			if a.Season != b.Season {
				return a.Season < b.Season
			}
			if a.Episode != b.Episode {
				return a.Episode < b.Episode
			}
			return a.Path < b.Path
		})
		catalog = append(catalog, group)
	}
	sort.Slice(catalog, func(i, j int) bool {
		a, b := strings.ToLower(catalog[i].Name), strings.ToLower(catalog[j].Name)
		if a == b {
			return catalog[i].ID < catalog[j].ID
		}
		return a < b
	})
	return catalog, partial
}

func joinLibraryPath(base, rel string) string {
	if base == "" {
		return rel
	}
	if rel == "" {
		return base
	}
	return base + "/" + rel
}

func displayVideoName(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ").Replace(name)), " ")
}

func libraryVideoLabel(filename, kind string) string {
	if kind == "series" {
		if match := videoEpisodePattern.FindStringIndex(filename); match != nil {
			remainder := strings.TrimLeft(filename[match[1]:], " ._-")
			if remainder != "" && !strings.HasPrefix(filename[match[1]:], filepath.Ext(filename)) {
				return displayVideoName(remainder)
			}
		}
	}
	return displayVideoName(filename)
}

func describeLibraryVideo(folder videoFolder, rel string) (string, string, int, int) {
	parts := strings.Split(rel, "/")
	name := parts[len(parts)-1]
	if folder.Kind == "movies" {
		return displayVideoName(name), joinLibraryPath(folder.Path, rel), 0, 0
	}
	title, group := filepath.Base(folder.Path), folder.Path
	if len(parts) > 1 && !videoSeasonPattern.MatchString(parts[0]) {
		title, group = parts[0], joinLibraryPath(folder.Path, parts[0])
	}
	if title == "." || title == "" {
		title = folder.Source
	}
	season, episode := 0, 0
	if match := videoEpisodePattern.FindStringSubmatch(name); match != nil {
		if match[1] == "" {
			match[1], match[2] = match[3], match[4]
		}
		season, _ = strconv.Atoi(match[1])
		episode, _ = strconv.Atoi(match[2])
	} else {
		for _, part := range parts[:len(parts)-1] {
			if match := videoSeasonPattern.FindStringSubmatch(part); match != nil {
				season, _ = strconv.Atoi(match[1])
			}
		}
		if match := videoEpisodeNumber.FindStringSubmatch(name); match != nil {
			episode, _ = strconv.Atoi(match[1])
		}
	}
	return strings.NewReplacer("_", " ", ".", " ").Replace(title), group, season, episode
}

func libraryCover(source, dir, video, kind string, access *folderAccess) string {
	if dir == "." {
		dir = ""
	}
	names := []string{"poster.jpg", "poster.png", "poster.webp", "folder.jpg", "cover.jpg"}
	if kind == "movies" {
		stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
		names = append([]string{stem + "-poster.jpg", stem + "-poster.png", stem + ".jpg", stem + ".png"}, names...)
	}
	for _, name := range names {
		relative := joinLibraryPath(dir, name)
		full, ok := access.readPath(source, relative)
		if !ok {
			continue
		}
		info, err := os.Stat(full)
		if err == nil && info.Mode().IsRegular() {
			return relative
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	return ""
}
