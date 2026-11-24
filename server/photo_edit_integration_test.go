package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Runs only with an explicit synthetic JPEG fixture and real libvips installed.
func TestRealPhotoEditsKeepEXIFAndRestoreExactBytes(t *testing.T) {
	fixture := os.Getenv("FILEHUB_EDIT_FIXTURE")
	if fixture == "" {
		t.Skip("set FILEHUB_EDIT_FIXTURE to a synthetic EXIF JPEG")
	}
	root := maintenanceFixture(t)
	full := filepath.Join(root, "photo.jpg")
	b, e := os.ReadFile(fixture)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(full, b, 0600); e != nil {
		t.Fatal(e)
	}
	if vipsPath == "" {
		t.Fatal("libvips required")
	}
	indexAddPath("T", full, "photo.jpg")
	original, e := hashFile(full)
	if e != nil {
		t.Fatal(e)
	}
	taken, _, _ := exifInfo(full)
	if taken == 0 {
		t.Fatal("fixture EXIF missing")
	}
	rr := httptest.NewRecorder()
	handleRotate(rr, httptest.NewRequest("POST", "/api/photo/rotate?source=T&path=photo.jpg&deg=90", nil))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if next, _, _ := exifInfo(full); next != taken {
		t.Fatal("rotate lost EXIF", taken, next)
	}
	width, height := vipsDims(full)
	if width != 32 || height != 64 {
		t.Fatal("rotation dimensions", width, height)
	}
	rr = httptest.NewRecorder()
	handleCrop(rr, httptest.NewRequest("POST", "/api/photo/crop?source=T&path=photo.jpg&x=0&y=0&w=0.5&h=0.5", nil))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if next, _, _ := exifInfo(full); next != taken {
		t.Fatal("crop lost EXIF", taken, next)
	}
	width, height = vipsDims(full)
	if width != 16 || height != 32 {
		t.Fatal("crop dimensions", width, height)
	}
	var id string
	if e = db.QueryRow(`SELECT id FROM photo_versions WHERE sha256=?`, original).Scan(&id); e != nil {
		t.Fatal(e)
	}
	rr = httptest.NewRecorder()
	handleVersionRestore(rr, httptest.NewRequest("POST", "/api/photo/restore?id="+id, nil))
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	hash, e := hashFile(full)
	if e != nil || hash != original {
		t.Fatal("restore not byte-identical", hash, e)
	}
}

func TestRealOCRAcceptsLargePhotoWithBoundedDownscale(t *testing.T) {
	fixture := os.Getenv("FILEHUB_EDIT_FIXTURE")
	if fixture == "" {
		t.Skip("set FILEHUB_EDIT_FIXTURE to a synthetic EXIF JPEG")
	}
	previousPreview := previewDir
	previewDir = t.TempDir()
	t.Cleanup(func() { previewDir = previousPreview })
	maintenanceFixture(t)
	if vipsthumbnailPath == "" || tessLangs == "" {
		t.Fatal("real libvips and tesseract required")
	}
	full := filepath.Join(t.TempDir(), "large-photo.jpg")
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(full, b, 0600); err != nil {
		t.Fatal(err)
	}
	// A valid JPEG with trailing padding exercises the previous 25 MB rejection.
	if err = os.Truncate(full, 26<<20); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	_, method, status := extractText(context.Background(), full, filepath.Base(full), st)
	if method != "ocr" || status > 1 {
		t.Fatal("large JPEG must reach bounded libvips + tesseract processing", method, status)
	}
}
