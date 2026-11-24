package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

var compatibilitySlots = make(chan struct{}, 1)

// Stream a browser-compatible derivative while a client is connected.
// The original is opened read-only. Inputs are limited to regular video files
// and the demuxers that parse them directly.
func handleVideoCompatible(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	query := r.URL.Query()
	source := query.Get("source")
	start, valid := playbackPosition(query.Get("start"))
	full, ok := safePath(source, query.Get("path"))
	if !valid || !ok || !isVideoThumb(full) {
		http.Error(w, "invalid video", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil || runtime.GOOS != "linux" {
		http.Error(w, "compatible playback unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case compatibilitySlots <- struct{}{}:
		defer func() { <-compatibilitySlots }()
	default:
		w.Header().Set("Retry-After", "3")
		http.Error(w, "video conversion busy", http.StatusServiceUnavailable)
		return
	}

	relative, err := filepath.Rel(byName[source], full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(byName[source])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	input, err := root.Open(relative)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer input.Close()
	if info, err := input.Stat(); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Hour)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,pipe", "-format_whitelist", "avi,mov,matroska,webm,ogg,asf,flv,mpeg",
		"-readrate", "1.5", "-ss", strconv.FormatFloat(start, 'f', 3, 64), "-threads", "2", "-i", "/proc/self/fd/3",
		"-map", "0:v:0", "-map", "0:a:0?", "-filter_threads", "1", "-vf",
		"scale=w='min(iw,1920)':h='min(ih,1080)':force_original_aspect_ratio=decrease:force_divisible_by=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "veryfast", "-threads", "2",
		"-crf", "23", "-force_key_frames", "expr:gte(t,n_forced*2)", "-c:a", "aac", "-ac", "2", "-b:a", "128k",
		"-movflags", "+empty_moov+default_base_moof+frag_keyframe", "-flush_packets", "1", "-f", "mp4", "pipe:1")
	cmd.ExtraFiles = []*os.File{input}
	stdout, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		http.Error(w, "video conversion failed to start", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	// Wait for the MP4 header so a failing decoder can return a real HTTP error.
	startup := time.AfterFunc(20*time.Second, cancel)
	buffer := make([]byte, 64<<10)
	n, err := io.ReadAtLeast(stdout, buffer, 8)
	startup.Stop()
	if err != nil {
		http.Error(w, "video could not be converted", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Accept-Ranges", "none")
	controller := http.NewResponseController(w)
	for n != 0 {
		if _, err := w.Write(buffer[:n]); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}
		n, _ = stdout.Read(buffer)
	}
}
