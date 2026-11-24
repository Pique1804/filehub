package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const videoSegmentSeconds = 6
const videoDemuxers = "avi,mov,matroska,webm,ogg,asf,flv,mpeg"

var hlsWorkers = make(chan struct{}, 2)

type videoIdentity struct {
	path string
	size int64
	mod  time.Time
}

var videoDurations = struct {
	sync.Mutex
	values map[videoIdentity]float64
}{values: make(map[videoIdentity]float64)}

func openHLSVideo(source, path string) (*os.File, videoIdentity, error) {
	full, ok := safePath(source, path)
	if !ok || !isVideoThumb(full) {
		return nil, videoIdentity{}, os.ErrNotExist
	}
	if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() {
		return nil, videoIdentity{}, os.ErrNotExist
	}
	root, err := os.OpenRoot(byName[source])
	if err != nil {
		return nil, videoIdentity{}, err
	}
	defer root.Close()
	relative, err := filepath.Rel(byName[source], full)
	if err != nil {
		return nil, videoIdentity{}, err
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, videoIdentity{}, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, videoIdentity{}, os.ErrNotExist
	}
	return file, videoIdentity{full, info.Size(), info.ModTime()}, nil
}

func probeVideoDuration(ctx context.Context, file *os.File, key videoIdentity) (float64, error) {
	videoDurations.Lock()
	duration, found := videoDurations.values[key]
	videoDurations.Unlock()
	if found {
		return duration, nil
	}
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe",
		"-format_whitelist", videoDemuxers, "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", "/proc/self/fd/3")
	cmd.ExtraFiles = []*os.File{file}
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	duration, valid := playbackPosition(strings.TrimSpace(string(out)))
	if !valid || duration <= 0 {
		return 0, fmt.Errorf("video duration unavailable")
	}
	videoDurations.Lock()
	if len(videoDurations.values) >= 256 {
		clear(videoDurations.values)
	}
	videoDurations.values[key] = duration
	videoDurations.Unlock()
	return duration, nil
}

func videoPlaylist(source, path string, duration float64) string {
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	fmt.Fprintf(&playlist, "#EXT-X-TARGETDURATION:%d\n", videoSegmentSeconds)
	query := url.Values{"source": {source}, "path": {path}}
	for index, start := 0, 0.0; start < duration; index, start = index+1, start+videoSegmentSeconds {
		if index > 0 {
			playlist.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		query.Set("segment", strconv.Itoa(index))
		fmt.Fprintf(&playlist, "#EXTINF:%.6f,\nsegment.ts?%s\n", math.Min(videoSegmentSeconds, duration-start), query.Encode())
	}
	playlist.WriteString("#EXT-X-ENDLIST\n")
	return playlist.String()
}

func encodeVideoSegment(ctx context.Context, file *os.File, start, duration float64) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,pipe", "-format_whitelist", videoDemuxers,
		"-ss", strconv.FormatFloat(start, 'f', 6, 64), "-threads", "2", "-i", "/proc/self/fd/3",
		"-t", strconv.FormatFloat(duration, 'f', 6, 64), "-map", "0:v:0", "-map", "0:a:0?",
		"-filter_threads", "1", "-vf",
		"fps=25,scale=w='min(iw,1920)':h='min(ih,1080)':force_original_aspect_ratio=decrease:force_divisible_by=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "veryfast", "-threads", "2",
		"-crf", "22", "-maxrate", "5M", "-bufsize", "5M", "-g", "150", "-bf", "0",
		"-c:a", "aac", "-ac", "2", "-ar", "48000", "-b:a", "160k", "-muxdelay", "0",
		"-f", "mpegts", "pipe:1")
	cmd.ExtraFiles = []*os.File{file}
	return cmd.Output()
}

// Each rendition segment is independently produced from the original at the
// requested position. Seeking never requires encoding the preceding minutes.
func handleVideoHLS(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if runtime.GOOS != "linux" {
		http.Error(w, "compatible playback unavailable", http.StatusServiceUnavailable)
		return
	}
	query := r.URL.Query()
	source, path := query.Get("source"), query.Get("path")
	file, key, err := openHLSVideo(source, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	select {
	case hlsWorkers <- struct{}{}:
		defer func() { <-hlsWorkers }()
	case <-ctx.Done():
		http.Error(w, "video conversion busy", http.StatusServiceUnavailable)
		return
	}
	duration, err := probeVideoDuration(ctx, file, key)
	if err != nil {
		http.Error(w, "video duration unavailable", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if r.URL.Path == "/api/video/hls/index.m3u8" {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, videoPlaylist(source, path, duration))
		return
	}
	index, err := strconv.Atoi(query.Get("segment"))
	if err != nil || index < 0 || index >= int(math.Ceil(duration/videoSegmentSeconds)) {
		http.NotFound(w, r)
		return
	}
	start := float64(index * videoSegmentSeconds)
	data, err := encodeVideoSegment(ctx, file, start, math.Min(videoSegmentSeconds, duration-start))
	if err != nil || len(data) == 0 {
		http.Error(w, "video segment unavailable", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeContent(w, r, "segment.ts", time.Time{}, bytes.NewReader(data))
}
