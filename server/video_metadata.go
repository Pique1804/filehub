package main

import (
	"encoding/json"
	"encoding/xml"
	"html"
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

type videoNFO struct {
	Title  string   `xml:"title"`
	Plot   string   `xml:"plot"`
	Year   int      `xml:"year"`
	Genres []string `xml:"genre"`
}

var videoYearSuffix = regexp.MustCompile(`\s*[([]((?:19|20)[0-9]{2})[)\]]\s*$`)
var videoMetadataTag = regexp.MustCompile(`<[^>]*>`)
var tvmazePosters = regexp.MustCompile(`^https://static\.tvmaze\.com/uploads/images/medium_portrait/[0-9]+/[0-9]+\.(jpg|png|webp)$`)

func videoMetadataText(value string, limit int) string {
	plain := html.UnescapeString(videoMetadataTag.ReplaceAllString(value, " "))
	runes := []rune(strings.Join(strings.Fields(plain), " "))
	return string(runes[:min(len(runes), limit)])
}

func applyLocalVideoMetadata(title *videoTitle, source, group, path string, access *folderAccess) {
	if match := videoYearSuffix.FindStringSubmatchIndex(title.Name); match != nil {
		title.Year, _ = strconv.Atoi(title.Name[match[2]:match[3]])
		title.Name = strings.TrimSpace(title.Name[:match[0]])
	}
	var sidecars []string
	if title.Kind == "series" {
		sidecars = []string{joinLibraryPath(group, "tvshow.nfo")}
	} else {
		sidecars = []string{strings.TrimSuffix(path, filepath.Ext(path)) + ".nfo"}
		if dir := filepath.ToSlash(filepath.Dir(path)); dir != "." && dir != "" {
			sidecars = append(sidecars, dir+"/movie.nfo")
		}
	}
	for _, sidecar := range sidecars {
		full, ok := access.readPath(source, sidecar)
		if !ok {
			continue
		}
		st, err := os.Lstat(full)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 128<<10 {
			continue
		}
		data, err := os.ReadFile(full)
		var nfo videoNFO
		if err != nil || xml.Unmarshal(data, &nfo) != nil {
			continue
		}
		if name := videoMetadataText(nfo.Title, 240); name != "" {
			title.Name = name
		}
		if nfo.Year > 0 {
			title.Year = nfo.Year
		}
		title.Description = videoMetadataText(nfo.Plot, 2500)
		for _, value := range nfo.Genres[:min(len(nfo.Genres), 12)] {
			if genre := videoMetadataText(value, 60); genre != "" {
				title.Genres = append(title.Genres, genre)
			}
		}
		break
	}
}

type showMetadata struct {
	description string
	year        int
	genres      []string
	poster      string
	link        string
}

type showQuery struct {
	title string
	year  int
}

type showCacheEntry struct {
	info    showMetadata
	expires time.Time
}

var tvmazeEnabled = env("FILEHUB_TVMAZE", "") == "1"
var tvmazeClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}
var tvmazeJobs = make(chan showQuery, 32)
var tvmazeOnce sync.Once
var tvmazeCache = struct {
	sync.Mutex
	values map[showQuery]showCacheEntry
}{values: make(map[showQuery]showCacheEntry)}

func titleSearchKey(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, title)
}

func lookupTVmaze(query showQuery) showMetadata {
	address := "https://api.tvmaze.com/search/shows?" + url.Values{"q": {query.title}}.Encode()
	req, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return showMetadata{}
	}
	req.Header.Set("User-Agent", "FileHub media library")
	response, err := tvmazeClient.Do(req)
	if err != nil {
		return showMetadata{}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return showMetadata{}
	}
	var matches []struct {
		Show struct {
			ID        int
			Name      string
			Premiered string
			Summary   string
			Genres    []string
			Image     struct{ Medium string }
		}
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&matches) != nil {
		return showMetadata{}
	}
	key := titleSearchKey(query.title)
	var info showMetadata
	for _, match := range matches {
		show := match.Show
		if show.ID <= 0 || titleSearchKey(show.Name) != key {
			continue
		}
		year := 0
		if len(show.Premiered) >= 4 {
			year, _ = strconv.Atoi(show.Premiered[:4])
		}
		if query.year > 0 && query.year != year {
			continue
		}
		if info.link != "" {
			// Exact title collisions must not silently choose a different series.
			return showMetadata{}
		}
		info = showMetadata{description: videoMetadataText(show.Summary, 2500), year: year,
			genres: show.Genres, link: "https://www.tvmaze.com/shows/" + strconv.Itoa(show.ID)}
		if tvmazePosters.MatchString(show.Image.Medium) {
			info.poster = show.Image.Medium
		}
	}
	return info
}

func applyOnlineVideoMetadata(title *videoTitle) {
	if !tvmazeEnabled || title.Kind != "series" || title.Name == "" || title.Description != "" {
		return
	}
	tvmazeOnce.Do(func() {
		go func() {
			for query := range tvmazeJobs {
				info := lookupTVmaze(query)
				lifetime := 24 * time.Hour
				if info.link == "" {
					lifetime = time.Hour
				}
				tvmazeCache.Lock()
				tvmazeCache.values[query] = showCacheEntry{info, time.Now().Add(lifetime)}
				tvmazeCache.Unlock()
				time.Sleep(time.Second)
			}
		}()
	})
	query := showQuery{videoMetadataText(title.Name, 160), title.Year}
	tvmazeCache.Lock()
	entry, found := tvmazeCache.values[query]
	if !found || time.Now().After(entry.expires) {
		select {
		case tvmazeJobs <- query:
			if len(tvmazeCache.values) >= 1024 {
				clear(tvmazeCache.values)
			}
			// Mark the lookup as queued so repeated catalog reads don't duplicate it.
			tvmazeCache.values[query] = showCacheEntry{expires: time.Now().Add(2 * time.Minute)}
		default:
		}
	}
	tvmazeCache.Unlock()
	if entry.info.link == "" {
		return
	}
	title.Description, title.MetadataLink, title.Poster = entry.info.description, entry.info.link, entry.info.poster
	title.MetadataSource = "tvmaze"
	if title.Year == 0 {
		title.Year = entry.info.year
	}
	if len(title.Genres) == 0 {
		title.Genres = entry.info.genres
	}
}
