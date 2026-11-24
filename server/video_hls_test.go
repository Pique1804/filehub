package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestVideoPlaylistHasFullSeekableDuration(t *testing.T) {
	playlist := videoPlaylist("Archive", "Shows/Лето & осень/E01.avi", 37.25)
	if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.HasSuffix(playlist, "#EXT-X-ENDLIST\n") {
		t.Fatalf("expected a finite playlist: %s", playlist)
	}
	if strings.Count(playlist, "#EXTINF:6.000000") != 6 || !strings.Contains(playlist, "#EXTINF:1.250000") {
		t.Fatalf("unexpected segment durations: %s", playlist)
	}
	lines := strings.Split(playlist, "\n")
	last, err := url.Parse(lines[len(lines)-3])
	if err != nil || last.Query().Get("segment") != "6" ||
		last.Query().Get("path") != "Shows/Лето & осень/E01.avi" {
		t.Fatalf("unexpected last segment: %v (%v)", last, err)
	}
}
