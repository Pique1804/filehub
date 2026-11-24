package main

import (
	"html"
	"net/http"
	"net/url"
	"strings"
)

// Both clients receive the same raster style. Photo coordinates remain in the
// authenticated Filehub API; the basemap provider only serves map tiles/fonts.
func mapStyle(tiles, attribution string) (map[string]any, bool) {
	u, err := url.Parse(tiles)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, false
	}
	for _, token := range []string{"{z}", "{x}", "{y}"} {
		if !strings.Contains(tiles, token) {
			return nil, false
		}
	}
	credit := html.EscapeString(attribution)
	if attribution == "© OpenStreetMap contributors" {
		credit = `<a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener noreferrer">© OpenStreetMap contributors</a>`
	}
	return map[string]any{
		"version": 8,
		"glyphs":  "https://demotiles.maplibre.org/font/{fontstack}/{range}.pbf",
		"sources": map[string]any{"basemap": map[string]any{
			"type": "raster", "tiles": []string{tiles}, "tileSize": 256,
			"maxzoom": 19, "attribution": credit,
		}},
		"layers": []map[string]any{{"id": "basemap", "type": "raster", "source": "basemap"}},
	}, true
}

func handleMapConfig(w http.ResponseWriter, r *http.Request) {
	style, ok := mapStyle(env("FILEHUB_MAP_TILES", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"),
		env("FILEHUB_MAP_ATTRIBUTION", "© OpenStreetMap contributors"))
	if !ok {
		http.Error(w, "Map source is not configured correctly", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeJSON(w, map[string]any{"style": style, "maxZoom": 19})
}
