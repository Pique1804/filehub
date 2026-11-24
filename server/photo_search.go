package main

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var photoSearchTokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// FTS5's unicode61 tokenizer does not fold Russian е/ё. Search both spellings
// of each word, with a small cap to avoid exponential query growth.
func photoSearchTerm(term string) string {
	runes := []rune(strings.ToLower(term))
	var positions []int
	for i, r := range runes {
		if r == 'е' || r == 'ё' {
			positions = append(positions, i)
		}
	}
	if len(positions) == 0 || len(positions) > 4 {
		return `"` + term + `"`
	}
	variants := make([]string, 0, 1<<len(positions))
	for mask := 0; mask < 1<<len(positions); mask++ {
		variant := append([]rune(nil), runes...)
		for bit, pos := range positions {
			if mask&(1<<bit) == 0 {
				variant[pos] = 'е'
			} else {
				variant[pos] = 'ё'
			}
		}
		variants = append(variants, `"`+string(variant)+`"`)
	}
	return "(" + strings.Join(variants, " OR ") + ")"
}

type photoSearchResponse struct {
	Photos     []Photo           `json:"photos"`
	Count      int               `json:"count"`
	Intent     *fileSearchIntent `json:"intent,omitempty"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// With mode=auto, photo/video and year constrain the scoped timeline. Without
// a filter the endpoint keeps its OCR-only behavior for free-text searches.
func handlePhotoTextSearch(w http.ResponseWriter, r *http.Request) {
	result := photoSearchResponse{Photos: []Photo{}}
	params := r.URL.Query()
	q := strings.TrimSpace(params.Get("q"))
	if utf8.RuneCountInString(q) < 2 || db == nil {
		writeJSON(w, result)
		return
	}
	text, intent := parseSearchIntent(q, params.Get("mode"), "image", "video")
	result.Intent = intent
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var album map[photoSearchKey]bool
	if id := params.Get("album"); id != "" {
		var err error
		album, err = photoSearchIDs(ctx, `SELECT source,path FROM album_photos WHERE album_id=?`, id, nil)
		if err != nil {
			http.Error(w, "album search unavailable", http.StatusInternalServerError)
			return
		}
	}

	// OCR means literal words visible in the image. Prefix matching "кот"*
	// also finds "который" and produces many unrelated screenshots.
	var terms []string
	for _, term := range photoSearchTokenRe.FindAllString(text, -1) {
		terms = append(terms, photoSearchTerm(term))
	}
	if intent == nil && len(terms) == 0 {
		writeJSON(w, result)
		return
	}
	yearFrom, yearBefore := int64(0), int64(0)
	if intent != nil && intent.Year != 0 {
		yearFrom, yearBefore = searchYearBounds(intent.Year, params.Get("zone"))
	}
	source, prefix := params.Get("source"), params.Get("path")
	candidates := make([]Photo, 0)
	eligible := make(map[photoSearchKey]bool)
	for _, p := range profilePhotos(r) {
		key := photoSearchKey{p.Source, p.Path}
		if (source != "" && p.Source != source) || !pathWithin(p.Path, prefix) ||
			(album != nil && !album[key]) || eligible[key] {
			continue
		}
		if intent != nil {
			if intent.Type != "" && p.IsVideo != (intent.Type == "video") {
				continue
			}
			if intent.Year != 0 && (p.ModTime < yearFrom || p.ModTime >= yearBefore) {
				continue
			}
		}
		candidates = append(candidates, p)
		eligible[key] = true
	}
	if text == "" {
		result.Photos = candidates
	} else {
		var ocr map[photoSearchKey]bool
		if len(terms) != 0 && len(candidates) != 0 {
			var err error
			ocr, err = photoSearchIDs(ctx, `SELECT source,path FROM content WHERE content MATCH ?`,
				strings.Join(terms, " AND "), eligible)
			if err != nil {
				log.Printf("filehub: photo OCR search: %v", err)
				http.Error(w, "photo text search unavailable", http.StatusInternalServerError)
				return
			}
		}
		nameText := strings.ToLower(text)
		for _, p := range candidates {
			if ocr[photoSearchKey{p.Source, p.Path}] || (intent != nil && strings.Contains(strings.ToLower(p.Name), nameText)) {
				result.Photos = append(result.Photos, p)
			}
		}
	}

	result.Count = len(result.Photos)
	limit := 500
	if v, err := strconv.Atoi(params.Get("limit")); err == nil && v > 0 && v < limit {
		limit = v
	}
	if cursor := params.Get("after"); cursor != "" {
		last, err := decodePhotoCursor(cursor)
		if err != nil {
			http.Error(w, "invalid photo cursor", http.StatusBadRequest)
			return
		}
		first := sort.Search(len(result.Photos), func(i int) bool { return photoBefore(last, result.Photos[i]) })
		result.Photos = result.Photos[first:]
	}
	if len(result.Photos) > limit {
		result.Photos = result.Photos[:limit]
		result.NextCursor = encodePhotoCursor(result.Photos[limit-1])
	}
	writeJSON(w, result)
}

type photoSearchKey struct{ source, path string }

func photoSearchIDs(ctx context.Context, query, arg string, allowed map[photoSearchKey]bool) (map[photoSearchKey]bool, error) {
	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matches := make(map[photoSearchKey]bool)
	for rows.Next() {
		var key photoSearchKey
		if err := rows.Scan(&key.source, &key.path); err != nil {
			return nil, err
		}
		if allowed == nil || allowed[key] {
			matches[key] = true
		}
	}
	return matches, rows.Err()
}
