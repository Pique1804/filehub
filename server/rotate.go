// Photo rotate: rotates the original image 90/180/270° via libvips and replaces it
// in place, then refreshes the index. Thumbnails auto-regenerate (their cache key
// includes mtime, which changes on rewrite).
package main

import (
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func handleRotate(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	q := r.URL.Query()
	source, path := q.Get("source"), q.Get("path")
	full, _, ok := resolveItem(source, path)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if !isThumbable(full) {
		http.Error(w, "not a rotatable image", http.StatusBadRequest)
		return
	}
	angle := map[string]string{"90": "d90", "180": "d180", "270": "d270"}[q.Get("deg")]
	if angle == "" {
		http.Error(w, "deg must be 90|180|270", http.StatusBadRequest)
		return
	}
	if vipsPath == "" {
		http.Error(w, "rotation unavailable (no vips)", http.StatusServiceUnavailable)
		return
	}
	ext := strings.ToLower(filepath.Ext(full))
	tmp, err := os.MkdirTemp(filepath.Dir(full), ".filehub-edit-*")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "rot"+ext)
	target := out
	if ext == ".jpg" || ext == ".jpeg" {
		target = out + "[Q=95]" // strip any now-stale EXIF orientation
	}
	// [autorotate] applies EXIF orientation FIRST so we rotate what the user actually
	// sees; then rot by the requested angle. Without it, rotating the raw pixels of an
	// EXIF-oriented photo nets no visible change.
	if err := exec.Command("vips", "rot", full+"[autorotate]", target, angle).Run(); err != nil {
		os.Remove(out)
		http.Error(w, "rotate failed", http.StatusInternalServerError)
		return
	}
	if err := replacePhoto(source, path, out, "rotate"); err != nil {
		os.Remove(out)
		http.Error(w, "replace failed", http.StatusInternalServerError)
		return
	}
	// keep the search index fresh (size/mtime changed on rewrite)

	writeJSON(w, map[string]any{"ok": true})
}

func vipsDims(p string) (int, int) {
	wb, _ := exec.Command("vipsheader", "-f", "width", p).Output()
	hb, _ := exec.Command("vipsheader", "-f", "height", p).Output()
	w, _ := strconv.Atoi(strings.TrimSpace(string(wb)))
	h, _ := strconv.Atoi(strings.TrimSpace(string(hb)))
	return w, h
}

// handleCrop crops the ORIGINAL full-res image (autorotated first so the crop matches
// what the user sees) using fractional x,y,w,h (0..1), then replaces the file in place.
func handleCrop(w http.ResponseWriter, r *http.Request) {
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	q := r.URL.Query()
	source, path := q.Get("source"), q.Get("path")
	full, _, ok := resolveItem(source, path)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if !isThumbable(full) {
		http.Error(w, "not a croppable image", http.StatusBadRequest)
		return
	}
	fx, _ := strconv.ParseFloat(q.Get("x"), 64)
	fy, _ := strconv.ParseFloat(q.Get("y"), 64)
	fw, _ := strconv.ParseFloat(q.Get("w"), 64)
	fh, _ := strconv.ParseFloat(q.Get("h"), 64)
	if math.IsNaN(fx+fy+fw+fh) || math.IsInf(fx+fy+fw+fh, 0) || fw <= 0 || fh <= 0 || fx < 0 || fy < 0 || fx >= 1 || fy >= 1 || fw > 1 || fh > 1 {
		http.Error(w, "bad crop rect", http.StatusBadRequest)
		return
	}
	if vipsPath == "" {
		http.Error(w, "crop unavailable (no vips)", http.StatusServiceUnavailable)
		return
	}
	ext := strings.ToLower(filepath.Ext(full))
	jpeg := ext == ".jpg" || ext == ".jpeg"
	// 1) autorotate to a temp (so fractions map to the displayed image)
	tmp, err := os.MkdirTemp(filepath.Dir(full), ".filehub-edit-*")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(tmp)
	ar := filepath.Join(tmp, "autorot.v")
	arT := ar
	if jpeg {
		arT = ar
	}
	if err := exec.Command("vips", "autorot", full, arT).Run(); err != nil {
		os.Remove(ar)
		http.Error(w, "prepare failed", http.StatusInternalServerError)
		return
	}
	W, H := vipsDims(ar)
	if W == 0 || H == 0 {
		os.Remove(ar)
		http.Error(w, "read dims failed", http.StatusInternalServerError)
		return
	}
	left, top := int(fx*float64(W)), int(fy*float64(H))
	cw, ch := int(fw*float64(W)), int(fh*float64(H))
	if left+cw > W {
		cw = W - left
	}
	if top+ch > H {
		ch = H - top
	}
	if cw < 1 || ch < 1 {
		os.Remove(ar)
		http.Error(w, "crop out of bounds", http.StatusBadRequest)
		return
	}
	// 2) extract_area <in> <out> left top width height
	out := filepath.Join(tmp, "crop"+ext)
	outT := out
	if jpeg {
		outT = out + "[Q=95]"
	}
	if err := exec.Command("vips", "extract_area", ar, outT,
		strconv.Itoa(left), strconv.Itoa(top), strconv.Itoa(cw), strconv.Itoa(ch)).Run(); err != nil {
		os.Remove(ar)
		os.Remove(out)
		http.Error(w, "crop failed", http.StatusInternalServerError)
		return
	}
	os.Remove(ar)
	if err := replacePhoto(source, path, out, "crop"); err != nil {
		os.Remove(out)
		http.Error(w, "replace failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"ok": true})
}
