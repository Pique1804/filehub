// On-the-fly image/video thumbnails with a disk cache.
// Images go through libvips (vipsthumbnail) when available — it DCT-shrinks JPEGs
// on load (decodes at reduced scale), so a 12 MP phone photo becomes a 256 px thumb
// in ~80 ms using little RAM. Falls back to a pure-Go ApproxBiLinear resize.
// ?w=<maxDim> picks the size (256 for the grid; bigger for the full-screen viewer).
// The whole library is pre-warmed in the background so scrolling is instant.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/sys/unix"
)

var thumbExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true,
	// HEIC/HEIF (iPhone photos): vips decodes them via libheif and we always serve
	// a JPEG thumbnail, so the timeline includes them and Android never sees raw HEIC.
	".heic": true, ".heif": true,
}

var videoExt = map[string]bool{
	".mp4": true, ".webm": true, ".mkv": true, ".mov": true, ".avi": true,
	".m4v": true, ".ogv": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true,
}

var thumbDir = env("FILEHUB_THUMB_DIR", "/opt/filehub/thumbs")

// gridThumbW MUST match the width the Android grid requests (one fixed size →
// the prewarm actually warms what the app asks for, and pinch-zoom doesn't refetch).
const gridThumbW = 192 // MUST equal the app's GRID_THUMB; 192 is ~3× DPI at ~160px cells but 4× cheaper to make/transfer than 320

// vipsPath is the vipsthumbnail binary if installed; "" => use the pure-Go path.
var vipsPath = func() string { p, _ := exec.LookPath("vipsthumbnail"); return p }()
var vipsHeaderPath = func() string { p, _ := exec.LookPath("vipsheader"); return p }()
var prlimitPath = func() string { p, _ := exec.LookPath("prlimit"); return p }()
var imageThumbWorker = make(chan struct{}, 1)

const (
	maxThumbnailInput  = 64 << 20
	maxNativePixels    = 100_000_000
	maxGoDecoderPixels = 20_000_000
)

func validThumbnailDimensions(width, height, budget int) bool {
	return width > 0 && height > 0 && width <= 32768 && height <= 32768 && width <= budget/height
}

func limitedVips(ctx context.Context, program string, args ...string) *exec.Cmd {
	options := []string{"--as=536870912", "--fsize=16777216", "--", program}
	cmd := exec.CommandContext(ctx, prlimitPath, append(options, args...)...)
	cmd.Env = append(os.Environ(), "VIPS_CONCURRENCY=1", "VIPS_DISC_THRESHOLD=1g")
	return cmd
}

func nativeImageThumb(ctx context.Context, input []byte, extension string, maxDim int) ([]byte, error) {
	dir, err := os.MkdirTemp(thumbDir, ".decode-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	source, output := filepath.Join(dir, "input"+extension), filepath.Join(dir, "thumb.jpg")
	if err := os.WriteFile(source, input, 0o600); err != nil {
		return nil, err
	}
	dimensions := [2]int{}
	for i, name := range []string{"width", "height"} {
		value, err := limitedVips(ctx, vipsHeaderPath, "-f", name, source).Output()
		if err != nil {
			return nil, err
		}
		dimensions[i], err = strconv.Atoi(strings.TrimSpace(string(value)))
		if err != nil {
			return nil, err
		}
	}
	if !validThumbnailDimensions(dimensions[0], dimensions[1], maxNativePixels) {
		return nil, errors.New("image exceeds thumbnail pixel limit")
	}
	cmd := limitedVips(ctx, vipsPath, source, "-s", fmt.Sprintf("%dx%d", maxDim, maxDim),
		"-o", output+"[Q=80,strip,optimize_coding=false,subsample_mode=on]")
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(output)
}

func isThumbable(name string) bool  { return thumbExt[strings.ToLower(filepath.Ext(name))] }
func isVideoThumb(name string) bool { return videoExt[strings.ToLower(filepath.Ext(name))] }

func clampDim(s string) int {
	d := 256
	if v, err := strconv.Atoi(s); err == nil {
		d = v
	}
	if d < 64 {
		d = 64
	}
	if d > 2048 {
		d = 2048
	}
	return d
}

func thumbCachePath(full string, st os.FileInfo, maxDim int) string {
	key := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d", full, st.ModTime().UnixNano(), st.Size(), maxDim)))
	return filepath.Join(thumbDir, hex.EncodeToString(key[:])+".jpg")
}

var videoThumbWorkers = make(chan struct{}, 2)

func ffmpegThumb(ctx context.Context, full string, maxDim int, second string) ([]byte, error) {
	scale := fmt.Sprintf("scale='min(%d,iw)':-1", maxDim)
	return exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-threads", "1",
		"-protocol_whitelist", "file,pipe", "-format_whitelist", videoDemuxers,
		"-ss", second, "-i", full, "-frames:v", "1", "-filter_threads", "1", "-vf", scale,
		"-threads", "1", "-q:v", "5", "-f", "mjpeg", "pipe:1").Output()
}

func darkVideoFrame(frame image.Image) bool {
	bounds := frame.Bounds()
	dark := 0
	const samples = 16
	for row := range samples {
		for col := range samples {
			x := bounds.Min.X + col*bounds.Dx()/samples
			y := bounds.Min.Y + row*bounds.Dy()/samples
			r, g, b, _ := frame.At(x, y).RGBA()
			luma := (299*(r>>8) + 587*(g>>8) + 114*(b>>8)) / 1000
			if luma < 28 {
				dark++
			}
		}
	}
	return dark*100 >= samples*samples*88
}

var errDarkVideoFrame = errors.New("video frame is too dark")

func genVideoFrame(ctx context.Context, full string, st os.FileInfo, maxDim int) ([]byte, error) {
	cache := thumbCachePath(full+"|video-frame-v1", st, maxDim)
	if data, err := os.ReadFile(cache); err == nil {
		if len(data) == 0 {
			return nil, errDarkVideoFrame
		}
		return data, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case videoThumbWorkers <- struct{}{}:
		defer func() { <-videoThumbWorkers }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Typically this skips the intro fade. Short videos may need an earlier frame.
	data, err := ffmpegThumb(ctx, full, maxDim, "12")
	if err == nil && len(data) == 0 {
		data, err = ffmpegThumb(ctx, full, maxDim, "1")
	}
	if err != nil {
		return nil, err
	}
	frame, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	os.MkdirAll(thumbDir, 0o755)
	if darkVideoFrame(frame) {
		// Remember a decoded dark frame. Transient decoder failures are not cached.
		_ = os.WriteFile(cache, nil, 0o644)
		return nil, errDarkVideoFrame
	}
	_ = os.WriteFile(cache, data, 0o644)
	return data, nil
}

// genThumb produces (and disk-caches) a JPEG thumbnail of max dimension maxDim.
func genThumb(ctx context.Context, full string, st os.FileInfo, maxDim int) ([]byte, error) {
	return buildThumb(ctx, full, st, maxDim, nil)
}

// A restricted caller can supply a pinned, symlink-free source file opener.
func buildThumb(ctx context.Context, full string, st os.FileInfo, maxDim int, open func() (*os.File, error)) ([]byte, error) {
	cachePath := thumbCachePath(full, st, maxDim)
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	os.MkdirAll(thumbDir, 0o755)

	// video → one frame via ffmpeg
	if open == nil && actualVideoFile(full) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		select {
		case videoThumbWorkers <- struct{}{}:
			defer func() { <-videoThumbWorkers }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if data, err := os.ReadFile(cachePath); err == nil {
			return data, nil
		}
		b, err := ffmpegThumb(ctx, full, maxDim, "1")
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || len(b) == 0 {
			return nil, fmt.Errorf("video thumb failed")
		}
		os.WriteFile(cachePath, b, 0o644)
		return b, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case imageThumbWorker <- struct{}{}:
		defer func() { <-imageThumbWorker }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	pinned := open != nil
	if open == nil {
		open = func() (*os.File, error) { return os.OpenFile(full, os.O_RDONLY|unix.O_NONBLOCK, 0) }
	}
	f, err := open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxThumbnailInput {
		return nil, errors.New("unsupported thumbnail input")
	}
	if pinned && photoFileStamp(info) != photoFileStamp(st) {
		return nil, errors.New("image changed before thumbnail generation")
	}
	// Both the header probe and the decoder must read the same bytes, even
	// when a writer modifies the original while a thumbnail is generated.
	input, err := io.ReadAll(io.LimitReader(f, maxThumbnailInput+1))
	if err != nil || len(input) > maxThumbnailInput {
		return nil, errors.New("thumbnail input exceeds byte limit")
	}
	config, _, configErr := image.DecodeConfig(bytes.NewReader(input))
	if configErr == nil && !validThumbnailDimensions(config.Width, config.Height, maxNativePixels) {
		return nil, errors.New("image exceeds thumbnail pixel limit")
	}
	if vipsPath != "" && vipsHeaderPath != "" && prlimitPath != "" && ctx.Err() == nil {
		if data, err := nativeImageThumb(ctx, input, strings.ToLower(filepath.Ext(full)), maxDim); err == nil && len(data) > 0 {
			_ = os.WriteFile(cachePath, data, 0o644)
			return data, nil
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if configErr != nil || !validThumbnailDimensions(config.Width, config.Height, maxGoDecoderPixels) {
		return nil, errors.New("image is not supported by the bounded thumbnail decoder")
	}
	src, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	tw, th := b.Dx(), b.Dy()
	if tw == 0 || th == 0 {
		return nil, fmt.Errorf("empty image")
	}
	if tw >= th && tw > maxDim {
		th = th * maxDim / tw
		tw = maxDim
	} else if th > tw && th > maxDim {
		tw = tw * maxDim / th
		th = maxDim
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(1, tw), max(1, th)))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var buf bytes.Buffer
	jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82})
	out := buf.Bytes()
	os.WriteFile(cachePath, out, 0o644)
	return out, nil
}

func handleThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	full, _, ok := resolveItem(q.Get("source"), q.Get("path"))
	document := q.Get("kind") == "document" && isPreviewable(full)
	videoFrame := q.Get("kind") == "video-frame"
	if !ok || (!document && !isThumbable(full) && !isVideoThumb(full)) {
		http.Error(w, "no thumb", http.StatusBadRequest)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if videoFrame && !actualVideoFile(full) {
		http.NotFound(w, r)
		return
	}
	dim := clampDim(q.Get("w"))
	if document {
		dim = min(dim, 384)
	}
	variant := ""
	if videoFrame {
		variant = "-frame-v1"
	}
	etag := fmt.Sprintf(`"%d-%d-%d%s"`, st.ModTime().UnixNano(), st.Size(), dim, variant)
	w.Header().Set("ETag", etag)
	if requestFolders(r.Context()) == nil {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	var out []byte
	if document {
		out, err = genDocumentThumb(r.Context(), full, st, dim)
	} else if videoFrame {
		out, err = genVideoFrame(r.Context(), full, st, dim)
	} else {
		out, err = genThumb(r.Context(), full, st, dim)
	}
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		w.Header().Del("ETag")
		if errors.Is(err, context.DeadlineExceeded) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "thumbnail busy", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "thumb failed", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")

	w.Write(out)
}

// prewarmThumbs generates grid thumbnails for the given photos in the
// background (bounded concurrency), skipping already-cached ones — used for the
// first screens of a /api/photos response so the first scroll is instant.
var prewarmSem = make(chan struct{}, 2)

func prewarmThumbs(photos []Photo) {
	go func() {
		for _, p := range photos {
			full, ok := safePath(p.Source, p.Path)
			if !ok {
				continue
			}
			st, err := os.Stat(full)
			if err != nil || st.IsDir() {
				continue
			}
			if _, err := os.Stat(thumbCachePath(full, st, gridThumbW)); err == nil {
				continue // already cached
			}
			prewarmSem <- struct{}{}
			go func(full string, st os.FileInfo) {
				defer func() { <-prewarmSem }()
				genThumb(context.Background(), full, st, gridThumbW)
			}(full, st)
		}
	}()
}

// prewarmAll pre-generates grid thumbnails for the ENTIRE media library from the
// index, so the gallery is fully cached and scrolls instantly. Skips cached ones,
// bounded to 2 workers (CT has 2 cores), and never runs two sweeps at once.
var prewarmBusy int32

func prewarmAll() {
	if db == nil || !atomic.CompareAndSwapInt32(&prewarmBusy, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&prewarmBusy, 0)

	rows, err := db.Query(`SELECT name,path,source,mtime FROM files WHERE isdir=0`)
	if err != nil {
		return
	}
	type item struct {
		source, path string
		date         int64
	}
	var items []item
	for rows.Next() {
		var name, path, src string
		var mt int64
		if rows.Scan(&name, &path, &src, &mt) != nil {
			continue
		}
		if photoNoise(path) || (!isThumbable(name) && !isVideoThumb(name)) {
			continue
		}
		if !inPhotoScope(src, path) {
			continue
		}
		items = append(items, item{src, path, captureDate(name, mt)})
	}
	rows.Close()
	// Only warm what the timeline actually shows (newest photosCap) — saves a lot of
	// disk I/O on a big library and focuses on what the user will scroll first.
	sort.Slice(items, func(i, j int) bool { return items[i].date > items[j].date })
	if len(items) > prewarmCap {
		items = items[:prewarmCap]
	}

	sem := make(chan struct{}, 2) // use both cores for the one-time backfill (OCR is gated off during prewarm)
	var wg sync.WaitGroup
	var generated int64
	metaStamps, _ := photoMetaStamps(db)
	for _, it := range items {
		full, ok := safePath(it.source, it.path)
		if !ok {
			continue
		}
		st, err := os.Stat(full)
		if err != nil || st.IsDir() {
			continue
		}
		_, cerr := os.Stat(thumbCachePath(full, st, gridThumbW))
		needThumb := cerr != nil
		needExif := isThumbable(it.path) && metaStamps[photoMetaID{it.source, it.path}] != photoFileStamp(st)
		if !needThumb && !needExif {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(source, path, full string, st os.FileInfo, nt, ne bool) {
			defer wg.Done()
			defer func() { <-sem }()
			if nt {
				if _, err := genThumb(context.Background(), full, st, gridThumbW); err == nil {
					atomic.AddInt64(&generated, 1)
				}
			}
			if ne { // cache real EXIF capture date + GPS
				indexPhotoMeta(source, path, full)
			}
		}(it.source, it.path, full, st, needThumb, needExif)
	}
	wg.Wait()
	log.Printf("filehub: thumbnail prewarm sweep done over %d media, %d newly generated", len(items), generated)
}
