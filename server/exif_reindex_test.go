package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReindexClearsLegacyPhotoMetadataForReplacementWithoutEXIF(t *testing.T) {
	root := maintenanceFixture(t)
	const name = "replaced.jpg"
	if err := os.WriteFile(filepath.Join(root, name), []byte{0xff, 0xd8, 0xff, 0xd9}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO photo_meta(source,path,taken,lat,lon) VALUES('T',?,1577836800,12,30)`, name); err != nil {
		t.Fatal(err)
	}
	if _, err := rebuildFileIndex(); err != nil {
		t.Fatal(err)
	}
	var taken int64
	var lat, lon float64
	var stamp string
	if err := db.QueryRow(`SELECT taken,lat,lon,file_stamp FROM photo_meta WHERE source='T' AND path=?`, name).Scan(&taken, &lat, &lon, &stamp); err != nil || taken != 0 || lat != 0 || lon != 0 || stamp == "" {
		t.Fatalf("metadata after external replacement: taken=%d latitude=%g longitude=%g stamp=%q error=%v", taken, lat, lon, stamp, err)
	}
}
