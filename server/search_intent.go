package main

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var searchTypeWords = map[string]string{
	"фото": "image", "фотографии": "image", "картинки": "image", "photo": "image", "photos": "image", "images": "image",
	"видео": "video", "ролики": "video", "video": "video", "videos": "video",
	"документы": "document", "документ": "document", "document": "document", "documents": "document",
	"архивы": "archive", "архив": "archive", "archive": "archive", "archives": "archive",
	"аудио": "audio", "музыка": "audio", "audio": "audio", "music": "audio",
	"папки": "folder", "папка": "folder", "folder": "folder", "folders": "folder",
	"files": "file", "файлы": "file",
}

type fileSearchIntent struct {
	Type             string `json:"type,omitempty"`
	Year             int    `json:"year,omitempty"`
	WithoutType      string `json:"withoutType"`
	WithoutYear      string `json:"withoutYear"`
	Extension        string `json:"extension,omitempty"`
	WithoutExtension string `json:"withoutExtension,omitempty"`
}

// A dotted token requests a suffix. A bare token is treated as an
// extension only when it is a recognized format and the only name term.
func parseFileExtension(raw, text string) (remaining, extension, without string) {
	words := strings.Fields(text)
	for index, word := range words {
		token := strings.ToLower(word)
		dotted := strings.HasPrefix(token, ".")
		token = strings.TrimPrefix(token, ".")
		if len(token) == 0 || len(token) > 16 || strings.IndexFunc(token, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) >= 0 {
			continue
		}
		ext := "." + token
		if !dotted && (len(words) != 1 || !isKnownSearchExtension(ext)) {
			continue
		}
		original := strings.Fields(raw)
		return searchWordsExcept(words, index), ext, searchWordsExcept(original, slices.Index(original, word))
	}
	return text, "", ""
}

func isKnownSearchExtension(ext string) bool {
	if thumbExt[ext] || videoExt[ext] || officeExt[ext] {
		return true
	}
	for _, extensions := range searchFileExtensions {
		if slices.Contains(extensions, ext) {
			return true
		}
	}
	return false
}

func parseSearchIntent(query, mode string, acceptedTypes ...string) (string, *fileSearchIntent) {
	if mode != "auto" && mode != "literal" {
		return query, nil
	}
	if len(query) >= 2 && strings.HasPrefix(query, `"`) && strings.HasSuffix(query, `"`) {
		return strings.TrimSpace(query[1 : len(query)-1]), nil
	}
	if mode != "auto" {
		return query, nil
	}
	tokens := strings.Fields(query)
	typeIndex, yearIndex, yearWord, years := -1, -1, -1, 0
	intent := fileSearchIntent{}
	for i, token := range tokens {
		kind := searchTypeWords[strings.ToLower(token)]
		if kind != "" && typeIndex == -1 && (len(acceptedTypes) == 0 || slices.Contains(acceptedTypes, kind)) {
			typeIndex, intent.Type = i, kind
		}
		number, err := strconv.Atoi(token)
		if err == nil && len(token) == 4 && number >= 1000 && number <= 9998 {
			years++
			yearIndex, intent.Year = i, number
		}
	}
	if years > 1 {
		// Multiple years are ambiguous. Leave them all in the name search.
		yearIndex, intent.Year = -1, 0
	}
	if yearIndex >= 0 {
		for _, near := range []int{yearIndex + 1, yearIndex - 1} {
			if near < 0 || near >= len(tokens) {
				continue
			}
			switch strings.ToLower(tokens[near]) {
			case "год", "года", "г", "г.", "year":
				yearWord = near
			}
			if yearWord >= 0 {
				break
			}
		}
	}
	if intent.Type == "" && intent.Year == 0 {
		return query, nil
	}
	intent.WithoutType = searchWordsExcept(tokens, typeIndex)
	intent.WithoutYear = searchWordsExcept(tokens, yearIndex, yearWord)
	return searchWordsExcept(tokens, typeIndex, yearIndex, yearWord), &intent
}

func searchWordsExcept(words []string, excluded ...int) string {
	var remaining []string
	for i, word := range words {
		if !slices.Contains(excluded, i) {
			remaining = append(remaining, word)
		}
	}
	return strings.Join(remaining, " ")
}

func (intent *fileSearchIntent) filters(zone string) url.Values {
	filters := url.Values{}
	if intent.Type != "" {
		filters.Set("type", intent.Type)
	}
	if intent.Year != 0 {
		from, before := searchYearBounds(intent.Year, zone)
		filters.Set("modifiedFrom", strconv.FormatInt(from, 10))
		filters.Set("modifiedBefore", strconv.FormatInt(before, 10))
	}
	return filters
}

func searchYearBounds(year int, zone string) (from, before int64) {
	location, err := time.LoadLocation(zone)
	if zone == "" || err != nil {
		location = time.UTC
	}
	first := time.Date(year, time.January, 1, 0, 0, 0, 0, location)
	return first.Unix(), first.AddDate(1, 0, 0).Unix()
}
