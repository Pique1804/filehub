package main

import "testing"

func TestVideoLibraryGroupsSeriesAndSeasonFolders(t *testing.T) {
	tests := []struct {
		root, file, title, group string
		season, episode          int
	}{
		{"series", "Shameless/Season_07/S07e03_Home.Sweet.avi", "Shameless", "series/Shameless", 7, 3},
		{"series/Shameless", "Season_07/03 Home Sweet.avi", "Shameless", "series/Shameless", 7, 3},
		{"series", "Example_Show/Season_02/An unusual title.mkv", "Example Show", "series/Example_Show", 2, 0},
	}
	for _, tt := range tests {
		title, group, season, episode := describeLibraryVideo(videoFolder{Path: tt.root, Kind: "series"}, tt.file)
		if title != tt.title || group != tt.group || season != tt.season || episode != tt.episode {
			t.Errorf("%s: got %q %q season=%d episode=%d", tt.file, title, group, season, episode)
		}
	}
}

func TestVideoPlaybackPositionRejectsInvalidSeconds(t *testing.T) {
	for _, invalid := range []string{"NaN", "Infinity", "-1", "100000", "text"} {
		if _, ok := playbackPosition(invalid); ok {
			t.Errorf("accepted %q", invalid)
		}
	}
	if seconds, ok := playbackPosition("12.125"); !ok || seconds != 12.125 {
		t.Fatalf("expected a valid resume position, got %v (%v)", seconds, ok)
	}
}
