// File details: size, mtime, mode, MIME, and (on demand) a streamed SHA-256.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

func handleInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	full, _, ok := resolveItem(q.Get("source"), q.Get("path"))
	if !ok {
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	mt := mime.TypeByExtension(filepath.Ext(full))
	if mt == "" {
		mt = "—"
	}
	out := map[string]any{
		"name":    st.Name(),
		"size":    st.Size(),
		"modTime": st.ModTime().Unix(),
		"isDir":   st.IsDir(),
		"mode":    st.Mode().String(),
		"mime":    mt,
		"source":  q.Get("source"),
		"path":    q.Get("path"),
	}
	if st.Mode().IsRegular() && db != nil {
		key := ownerPath{q.Get("source"), q.Get("path")}
		if name := fileOwnerLabels(r.Context(), []ownerPath{key}, false)[key]; name != "" {
			out["owner"] = name
		}
		if account, ok := requestProfile(r); ok && account.role == "admin" {
			if personal, err := personalFolderOwners(r.Context()); err == nil {
				if _, within := inheritedOwner(key, personal); !within {
					if identity, err := ownerObject(key, false); err == nil {
						out["ownerEditToken"] = fileOwnerToken(identity)
					}
				}
			}
		}
	}
	// EXIF capture date + GPS + camera + dimensions for images (cheap — header only).
	if !st.IsDir() && isThumbable(full) {
		if taken, lat, lon := exifInfo(full); taken > 0 || lat != 0 || lon != 0 {
			if taken > 0 {
				out["taken"] = taken
			}
			if lat != 0 || lon != 0 {
				out["lat"] = lat
				out["lon"] = lon
			}
		}
		if cam := exifCamera(full); cam != "" {
			out["camera"] = cam
		}
		if f, err := os.Open(full); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				out["width"] = cfg.Width
				out["height"] = cfg.Height
			}
			f.Close()
		}
	}
	// SHA-256 is opt-in (streamed) so opening info on a huge file stays instant.
	if q.Get("hash") == "1" && !st.IsDir() {
		if f, err := os.Open(full); err == nil {
			h := sha256.New()
			io.Copy(h, f)
			f.Close()
			out["sha256"] = hex.EncodeToString(h.Sum(nil))
		}
	}
	writeJSON(w, out)
}
