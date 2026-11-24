package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	tmdbAPI    = "https://api.themoviedb.org/3"
	tmdbImages = "https://image.tmdb.org/t/p/w500"
	tmdbSite   = "https://www.themoviedb.org/"
)

var libraryTMDB = tmdbProvider{
	token: strings.TrimSpace(env("FILEHUB_TMDB_TOKEN", "")), language: env("FILEHUB_TMDB_LANGUAGE", "en-US"),
	credentialFile: filepath.Join(filepath.Dir(env("FILEHUB_DB", "/opt/filehub/index.db")), ".filehub-tmdb.json"),
	base:           tmdbAPI, client: &http.Client{
		Timeout:       6 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	},
	jobs: make(chan tmdbJob, 128), queued: make(map[string]struct{}),
}

type tmdbProvider struct {
	token, language, base string
	credentialFile        string
	client                *http.Client
	once                  sync.Once
	mu                    sync.Mutex
	jobs                  chan tmdbJob
	queued                map[string]struct{}
	retryAfter            time.Time
}

type tmdbJob struct {
	id, kind, query, lookup string
	year                    int
}

type tmdbRecord struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Original    string   `json:"original,omitempty"`
	Year        int      `json:"year,omitempty"`
	Poster      string   `json:"poster,omitempty"`
	Description string   `json:"description,omitempty"`
	Genres      []string `json:"genres,omitempty"`
}

type tmdbResponseItem struct {
	ID            int    `json:"id"`
	Adult         bool   `json:"adult"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
}

var tmdbPosterPath = regexp.MustCompile(`^/[A-Za-z0-9_-]{1,120}\.(jpg|png|webp)$`)
var releaseTag = regexp.MustCompile(`(?i)[\s(\[-]+(?:2160p|1080[pi]|720p|480p|4k|blu[ -]?ray|bdrip|brrip|webrip|web[ -]?dl|hdtv|dvdrip|remux|hevc|x26[45])\b`)
var bareReleaseYear = regexp.MustCompile(`\s+((?:19|20)[0-9]{2})$`)
var tmdbKeyV3 = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
var tmdbReadToken = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

type tmdbSettings struct {
	Credential string `json:"credential"`
}

func (p *tmdbProvider) credential() string {
	if p.credentialFile == "" {
		return p.token
	}
	info, err := os.Lstat(p.credentialFile)
	if errors.Is(err, os.ErrNotExist) {
		return p.token
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return ""
	}
	data, err := os.ReadFile(p.credentialFile)
	var settings tmdbSettings
	if err != nil || json.Unmarshal(data, &settings) != nil {
		return ""
	}
	return settings.Credential
}

func (p *tmdbProvider) saveCredential(credential string) error {
	temp, err := os.CreateTemp(filepath.Dir(p.credentialFile), ".filehub-tmdb-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := json.NewEncoder(temp).Encode(tmdbSettings{Credential: credential}); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), p.credentialFile); err != nil {
		return err
	}
	p.mu.Lock()
	p.retryAfter = time.Time{}
	p.mu.Unlock()
	return nil
}

func tmdbKind(kind string) string {
	if kind == "series" {
		return "tv"
	}
	return "movie"
}

func tmdbQueryFor(item *videoTitle) (string, int) {
	name, year := item.Name, item.Year
	// A release-tag suffix makes a preceding bare year credible. Preserve
	// numeric words in titles without such evidence (e.g. Blade Runner 2049).
	if suffix := releaseTag.FindStringIndex(name); suffix != nil && suffix[0] > 0 {
		name = strings.TrimSpace(name[:suffix[0]])
		match := videoYearSuffix.FindStringSubmatchIndex(name)
		if match == nil {
			match = bareReleaseYear.FindStringSubmatchIndex(name)
		}
		if match != nil {
			if year == 0 {
				year, _ = strconv.Atoi(name[match[2]:match[3]])
			}
			name = strings.TrimSpace(name[:match[0]])
		}
	}
	return videoMetadataText(name, 160), year
}

func tmdbNormalized(name string) string {
	return strings.Map(func(r rune) rune {
		if r == 'ё' || r == 'Ё' {
			return 'е'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}

func tmdbExactMatch(query string, year int, results []tmdbRecord) tmdbRecord {
	normalized := tmdbNormalized(query)
	if normalized == "" {
		return tmdbRecord{}
	}
	var match tmdbRecord
	for _, result := range results {
		if year != 0 && year != result.Year {
			continue
		}
		if normalized != tmdbNormalized(result.Name) && normalized != tmdbNormalized(result.Original) {
			continue
		}
		if match.ID != 0 && match.ID != result.ID {
			return tmdbRecord{}
		}
		match = result
	}
	return match
}

func (r tmdbResponseItem) record(kind string) tmdbRecord {
	name, original, date := r.Title, r.OriginalTitle, r.ReleaseDate
	if kind == "tv" {
		name, original, date = r.Name, r.OriginalName, r.FirstAirDate
	}
	record := tmdbRecord{ID: r.ID, Name: videoMetadataText(name, 240), Original: videoMetadataText(original, 240),
		Description: videoMetadataText(r.Overview, 2500)}
	if len(date) >= 4 {
		record.Year, _ = strconv.Atoi(date[:4])
	}
	if tmdbPosterPath.MatchString(r.PosterPath) {
		record.Poster = tmdbImages + r.PosterPath
	}
	for _, genre := range r.Genres[:min(len(r.Genres), 12)] {
		if name := videoMetadataText(genre.Name, 60); name != "" {
			record.Genres = append(record.Genres, name)
		}
	}
	return record
}

type tmdbHTTPError int

func (tmdbHTTPError) Error() string { return "TMDB returned an error" }

func (p *tmdbProvider) request(ctx context.Context, credential, endpoint string, values url.Values, destination any) error {
	if credential == "" {
		return errors.New("TMDB is not configured")
	}
	legacy := tmdbKeyV3.MatchString(credential)
	if legacy {
		values.Set("api_key", credential)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return errors.New("invalid TMDB request")
	}
	req.Header.Set("Accept", "application/json")
	if !legacy {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return errors.New("TMDB connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return tmdbHTTPError(response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(destination)
}

func (p *tmdbProvider) search(ctx context.Context, kind, query string, year int) ([]tmdbRecord, error) {
	values := url.Values{"query": {query}, "include_adult": {"false"}, "language": {p.language}}
	if year > 0 {
		field := "primary_release_year"
		if kind == "tv" {
			field = "first_air_date_year"
		}
		values.Set(field, strconv.Itoa(year))
	}
	var response struct {
		Results []tmdbResponseItem `json:"results"`
	}
	if err := p.request(ctx, p.credential(), "/search/"+kind, values, &response); err != nil {
		return nil, err
	}
	results := []tmdbRecord{}
	for _, item := range response.Results[:min(len(response.Results), 20)] {
		if item.ID > 0 && !item.Adult {
			results = append(results, item.record(kind))
		}
	}
	return results, nil
}

func (p *tmdbProvider) fetchDetails(ctx context.Context, kind string, id int) (tmdbRecord, error) {
	var item tmdbResponseItem
	err := p.request(ctx, p.credential(), "/"+kind+"/"+strconv.Itoa(id), url.Values{"language": {p.language}}, &item)
	if err != nil || item.ID != id || item.Adult {
		return tmdbRecord{}, errors.New("TMDB item unavailable")
	}
	return item.record(kind), nil
}

func (p *tmdbProvider) lookupKey(query string, year int) string {
	return p.language + "|" + strconv.Itoa(year) + "|" + query
}

func (p *tmdbProvider) enqueue(job tmdbJob) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.queued[job.id]; ok {
		return true
	}
	if time.Now().Before(p.retryAfter) {
		return false
	}
	select {
	case p.jobs <- job:
		p.queued[job.id] = struct{}{}
		p.once.Do(func() { go p.work() })
		return true
	default:
		return false
	}
}

func (p *tmdbProvider) work() {
	for job := range p.jobs {
		p.mu.Lock()
		canRequest := !time.Now().Before(p.retryAfter)
		p.mu.Unlock()
		if canRequest {
			results, err := p.search(context.Background(), job.kind, job.query, job.year)
			if err == nil {
				record := tmdbExactMatch(job.query, job.year, results)
				data, _ := json.Marshal(record)
				// A slow automatic lookup must never replace an administrator's choice.
				if db != nil {
					_, _ = db.Exec(`INSERT INTO video_tmdb_matches VALUES(?,?,?,?,0)
						ON CONFLICT(title_id) DO UPDATE SET lookup=excluded.lookup,result=excluded.result,updated=excluded.updated
						WHERE video_tmdb_matches.manual=0`, job.id, job.lookup, string(data), time.Now().Unix())
				}
			} else {
				p.mu.Lock()
				p.retryAfter = time.Now().Add(time.Minute)
				p.mu.Unlock()
			}
		}
		p.mu.Lock()
		delete(p.queued, job.id)
		p.mu.Unlock()
		time.Sleep(250 * time.Millisecond)
	}
}

type tmdbCached struct {
	lookup  string
	result  tmdbRecord
	updated int64
	manual  bool
}

func (p *tmdbProvider) enrich(ctx context.Context, titles []*videoTitle) bool {
	if len(titles) == 0 || db == nil {
		return false
	}
	ids := make([]string, 0, len(titles))
	for _, item := range titles {
		ids = append(ids, item.ID)
	}
	encoded, _ := json.Marshal(ids)
	rows, err := db.QueryContext(ctx, `SELECT title_id,lookup,result,updated,manual FROM video_tmdb_matches
		WHERE title_id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return false
	}
	cached := make(map[string]tmdbCached)
	for rows.Next() {
		var id, data string
		var entry tmdbCached
		if rows.Scan(&id, &entry.lookup, &data, &entry.updated, &entry.manual) == nil &&
			json.Unmarshal([]byte(data), &entry.result) == nil {
			cached[id] = entry
		}
	}
	rows.Close()

	now, pending := time.Now(), false
	for _, item := range titles {
		query, year := tmdbQueryFor(item)
		if query != item.Name {
			item.TMDBQuery = query
		}
		item.TMDBQueryYear = year
		lookup := p.lookupKey(query, year)
		entry := cached[item.ID]
		if entry.manual || entry.lookup == lookup {
			applyTMDBRecord(item, entry.result)
			lifetime := 7 * 24 * time.Hour
			if entry.result.ID == 0 {
				lifetime = 24 * time.Hour
			}
			if entry.manual || now.Sub(time.Unix(entry.updated, 0)) < lifetime {
				continue
			}
		}
		if query != "" && p.enqueue(tmdbJob{item.ID, tmdbKind(item.Kind), query, lookup, year}) {
			pending = true
		}
	}
	return pending
}

func applyTMDBRecord(item *videoTitle, record tmdbRecord) {
	if record.ID == 0 {
		item.TMDBNeedsMatch = true
		return
	}
	item.TMDBMatchID = record.ID
	item.MetadataSource = "tmdb"
	item.MetadataLink = tmdbSite + tmdbKind(item.Kind) + "/" + strconv.Itoa(record.ID)
	// The client prefers the local cover. Preserve descriptions and genres from NFO.
	item.Poster = record.Poster
	if item.Description == "" {
		item.Description = record.Description
	}
	if record.Year > 0 {
		item.Year = record.Year
	}
	if len(item.Genres) == 0 {
		item.Genres = record.Genres
	}
}

func tmdbTitle(w http.ResponseWriter, r *http.Request, id string) *videoTitle {
	if libraryTMDB.credential() == "" {
		http.Error(w, "TMDB is not configured", http.StatusServiceUnavailable)
		return nil
	}
	if len(id) == 0 || len(id) > 4096 {
		http.Error(w, "invalid library item", http.StatusBadRequest)
		return nil
	}
	select {
	case videoScans <- struct{}{}:
		defer func() { <-videoScans }()
	default:
		http.Error(w, "library busy", http.StatusServiceUnavailable)
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	folders, err := readVideoFolders(ctx)
	if err != nil {
		http.Error(w, "could not read video library", http.StatusInternalServerError)
		return nil
	}
	items, _ := scanVideoLibrary(ctx, clipVideoFolders(folders, requestFolders(ctx)), nil)
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	http.Error(w, "video library item unavailable", http.StatusNotFound)
	return nil
}

func handleTMDBSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, map[string]bool{"configured": libraryTMDB.credential() != ""})
}

func handleTMDBSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	credential := strings.TrimSpace(r.PostFormValue("credential"))
	switch r.PostFormValue("action") {
	case "save":
		if len(credential) > 2048 || (!tmdbKeyV3.MatchString(credential) && !tmdbReadToken.MatchString(credential)) {
			http.Error(w, "invalid TMDB credential", http.StatusBadRequest)
			return
		}
		var configuration struct {
			Images struct {
				SecureBaseURL string `json:"secure_base_url"`
			} `json:"images"`
		}
		err := libraryTMDB.request(r.Context(), credential, "/configuration", url.Values{}, &configuration)
		if err == tmdbHTTPError(http.StatusUnauthorized) {
			http.Error(w, "invalid TMDB credential", http.StatusUnprocessableEntity)
			return
		}
		if err != nil || configuration.Images.SecureBaseURL == "" {
			http.Error(w, "could not validate TMDB credential", http.StatusBadGateway)
			return
		}
	case "disable":
		credential = ""
	default:
		http.Error(w, "invalid TMDB settings action", http.StatusBadRequest)
		return
	}
	if err := libraryTMDB.saveCredential(credential); err != nil {
		http.Error(w, "could not save TMDB settings", http.StatusInternalServerError)
		return
	}
	handleTMDBSettings(w, r)
}

func handleTMDBSearch(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	item := tmdbTitle(w, r, params.Get("title"))
	if item == nil {
		return
	}
	query := videoMetadataText(params.Get("q"), 160)
	if query == "" {
		http.Error(w, "search name required", http.StatusBadRequest)
		return
	}
	year, _ := strconv.Atoi(params.Get("year"))
	if year < 1000 || year > 9999 {
		year = 0
	}
	results, err := libraryTMDB.search(r.Context(), tmdbKind(item.Kind), query, year)
	if err != nil {
		http.Error(w, "TMDB search unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, results)
}

func handleTMDBMatch(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	id, err := strconv.Atoi(r.PostFormValue("match"))
	if err != nil || id <= 0 {
		http.Error(w, "invalid TMDB item", http.StatusBadRequest)
		return
	}
	item := tmdbTitle(w, r, r.PostFormValue("title"))
	if item == nil {
		return
	}
	record, err := libraryTMDB.fetchDetails(r.Context(), tmdbKind(item.Kind), id)
	if err != nil {
		http.Error(w, "TMDB item unavailable", http.StatusBadGateway)
		return
	}
	data, _ := json.Marshal(record)
	query, year := tmdbQueryFor(item)
	if _, err := db.ExecContext(r.Context(), `INSERT INTO video_tmdb_matches VALUES(?,?,?,?,1)
		ON CONFLICT(title_id) DO UPDATE SET lookup=excluded.lookup,result=excluded.result,updated=excluded.updated,manual=1`,
		item.ID, libraryTMDB.lookupKey(query, year), string(data), time.Now().Unix()); err != nil {
		http.Error(w, "could not save the library match", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
