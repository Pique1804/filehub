package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A real JPEG with a standards-shaped TIFF EXIF sub-IFD. No household images.
func syntheticExifJPEG(offset string) []byte {
	date := []byte("2024:01:02 00:15:00\x00")
	zone := append([]byte(offset), 0)
	raw := make([]byte, 56+len(date)+len(zone))
	order := binary.LittleEndian
	copy(raw, "II")
	order.PutUint16(raw[2:], 42)
	order.PutUint32(raw[4:], 8)
	order.PutUint16(raw[8:], 1)
	order.PutUint16(raw[10:], 0x8769)
	order.PutUint16(raw[12:], 4)
	order.PutUint32(raw[14:], 1)
	order.PutUint32(raw[18:], 26)
	order.PutUint16(raw[26:], 2)
	order.PutUint16(raw[28:], 0x9003)
	order.PutUint16(raw[30:], 2)
	order.PutUint32(raw[32:], uint32(len(date)))
	order.PutUint32(raw[36:], 56)
	order.PutUint16(raw[40:], 0x9011)
	order.PutUint16(raw[42:], 2)
	order.PutUint32(raw[44:], uint32(len(zone)))
	if len(zone) > 4 {
		order.PutUint32(raw[48:], uint32(56+len(date)))
		copy(raw[56+len(date):], zone)
	} else {
		copy(raw[48:52], zone)
	}
	copy(raw[56:], date)
	var img bytes.Buffer
	jpeg.Encode(&img, image.NewRGBA(image.Rect(0, 0, 64, 32)), nil)
	payload := append([]byte("Exif\x00\x00"), raw...)
	out := []byte{0xff, 0xd8, 0xff, 0xe1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	out = append(out, payload...)
	return append(out, img.Bytes()[2:]...)
}
func TestEXIFCaptureOffsetAcrossDateBoundary(t *testing.T) {
	for _, zone := range []string{"+03:00", "-07:30", "+00:00", "bad", ""} {
		t.Run(zone, func(t *testing.T) {
			b := syntheticExifJPEG(zone)
			p := filepath.Join(t.TempDir(), "offset.jpg")
			os.WriteFile(p, b, 0600)
			want, err := time.Parse("2006:01:02 15:04:05-07:00", "2024:01:02 00:15:00"+zone)
			if err != nil {
				want, _ = time.ParseInLocation("2006:01:02 15:04:05", "2024:01:02 00:15:00", time.Local)
			}
			got, _, _ := exifInfo(p)
			if got != want.Unix() {
				t.Fatalf("offset %q: got %v want %v", zone, time.Unix(got, 0), want)
			}
		})
	}
	if out := os.Getenv("FILEHUB_GENERATED_EDIT_FIXTURE"); out != "" {
		if e := os.WriteFile(out, syntheticExifJPEG("+03:00"), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
