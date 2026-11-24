// Real capture date + GPS from image EXIF, cached in a small photo_meta table so
// the timeline can group by when a photo was TAKEN (camera/WhatsApp/edited copies
// that have no date in their filename) instead of when it was copied to the disk.
// EXIF is read once per photo during the thumbnail prewarm (goexif reads only the
// header, not the whole image). files is an FTS5 table (can't add columns), hence
// the sidecar table.
package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
)

// exifCamera returns a "Make Model" camera string from EXIF, or "".
func exifCamera(full string) string {
	f, err := os.Open(full)
	if err != nil {
		return ""
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return ""
	}
	g := func(field exif.FieldName) string {
		if t, e := x.Get(field); e == nil {
			if s, e := t.StringVal(); e == nil {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	mk, md := g(exif.Make), g(exif.Model)
	switch {
	case md == "":
		return mk
	case mk == "" || strings.HasPrefix(md, mk):
		return md
	default:
		return mk + " " + md
	}
}

func initPhotoMeta() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS photo_meta(
		source TEXT, path TEXT, taken INTEGER, lat REAL, lon REAL, PRIMARY KEY(source,path))`)
	if err != nil {
		return err
	}
	var stampColumn int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('photo_meta') WHERE name='file_stamp'`).Scan(&stampColumn); err != nil {
		return err
	}
	if stampColumn == 0 {
		_, err = db.Exec(`ALTER TABLE photo_meta ADD COLUMN file_stamp TEXT NOT NULL DEFAULT ''`)
	}
	return err
}

// exifInfo honors the EXIF capture offset; offsetless legacy photos retain the server-local interpretation.
func exifInfo(full string) (taken int64, lat, lon float64) {
	f, err := os.Open(full)
	if err != nil {
		return
	}
	defer f.Close()
	return decodePhotoEXIF(f)
}

func decodePhotoEXIF(reader io.Reader) (taken int64, lat, lon float64) {
	x, err := exif.Decode(reader)
	if err != nil {
		return
	}
	tg, err := x.Get(exif.DateTimeOriginal)
	offsetTag := uint16(0x9011) // OffsetTimeOriginal
	if err != nil {
		tg, err = x.Get(exif.DateTime)
		offsetTag = 0x9010
	}
	if err == nil {
		if s, err := tg.StringVal(); err == nil {
			s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
			t, e := time.ParseInLocation("2006:01:02 15:04:05", s, time.Local)
			if offset := exifTimeOffset(x, offsetTag); offset != "" {
				if withOffset, err := time.Parse("2006:01:02 15:04:05-07:00", s+offset); err == nil {
					t, e = withOffset, nil
				}
			}
			if e == nil && t.Year() >= 1990 {
				taken = t.Unix()
			}
		}
	}
	if la, lo, err := x.LatLong(); err == nil {
		lat, lon = la, lo
	}
	return
}

// goexif predates EXIF 2.31 and drops unknown offset fields. Read only the
// explicit EXIF sub-IFD with its own TIFF byte order, without changing parsers.
func exifTimeOffset(x *exif.Exif, id uint16) string {
	if x == nil || x.Tiff == nil {
		return ""
	}
	dirs := x.Tiff.Dirs
	if tag, err := x.Get(exif.ExifIFDPointer); err == nil {
		if offset, err := tag.Int64(0); err == nil && offset >= 0 && offset < int64(len(x.Raw)) {
			r := bytes.NewReader(x.Raw)
			r.Seek(offset, 0)
			if dir, _, err := tiff.DecodeDir(r, x.Tiff.Order); err == nil {
				dirs = append(append([]*tiff.Dir{}, dirs...), dir)
			}
		}
	}
	for _, dir := range dirs {
		for _, tag := range dir.Tags {
			if tag.Id == id {
				if val, err := tag.StringVal(); err == nil {
					return strings.TrimSpace(strings.TrimRight(val, "\x00"))
				}
			}
		}
	}
	return ""
}

type photoMetaID struct{ source, path string }

func photoFileStamp(info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d:%d:%d:%d:%d", st.Dev, st.Ino, st.Ctim.Sec, st.Ctim.Nsec, info.Size(), info.ModTime().UnixNano())
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

const upsertPhotoMeta = `INSERT INTO photo_meta(source,path,taken,lat,lon,file_stamp) VALUES(?,?,?,?,?,?)
	ON CONFLICT(source,path) DO UPDATE SET taken=excluded.taken,lat=excluded.lat,lon=excluded.lon,file_stamp=excluded.file_stamp`

func readPhotoMeta(source, path, stamp string) (taken int64, lat, lon float64, ok bool) {
	f, err := openArchiveFile(byName[source], path)
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	if before, err := f.Stat(); err != nil || photoFileStamp(before) != stamp {
		return 0, 0, 0, false
	}
	taken, lat, lon = decodePhotoEXIF(f)
	after, err := f.Stat()
	return taken, lat, lon, err == nil && photoFileStamp(after) == stamp
}

func indexPhotoMeta(source, path, full string) {
	if db == nil {
		return
	}
	before, err := os.Stat(full)
	if err != nil || !before.Mode().IsRegular() {
		return
	}
	stamp := photoFileStamp(before)
	taken, lat, lon, ok := readPhotoMeta(source, path, stamp)
	if !ok {
		return
	}
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	if current, err := os.Stat(full); err != nil || photoFileStamp(current) != stamp {
		return
	}
	if _, err := db.Exec(upsertPhotoMeta, source, path, taken, lat, lon, stamp); err == nil {
		invalidateMediaCaches()
	}
}

func photoMetaStamps(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}) (map[photoMetaID]string, error) {
	m := map[photoMetaID]string{}
	rows, err := queryer.Query(`SELECT source,path,file_stamp FROM photo_meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id photoMetaID
		var stamp string
		if err := rows.Scan(&id.source, &id.path, &stamp); err != nil {
			return nil, err
		}
		m[id] = stamp
	}
	return m, rows.Err()
}

// photoTakenMap returns source|path -> capture-date for all photos with known EXIF.
func photoTakenMap() map[string]int64 {
	m := map[string]int64{}
	if db == nil {
		return m
	}
	rows, err := db.Query(`SELECT source,path,taken FROM photo_meta WHERE taken>0`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var s, p string
		var t int64
		if rows.Scan(&s, &p, &t) == nil {
			m[s+"|"+p] = t
		}
	}
	return m
}
