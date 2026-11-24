// In-app update: serves the latest Android APK + a version manifest so the app
// can self-update. Files live under FILEHUB_APP_DIR (app.apk + version.json),
// published by the Android build step.
package main

import (
	"net/http"
	"os"
	"path/filepath"
)

var appDir = env("FILEHUB_APP_DIR", "/opt/filehub/app")

func handleAppLatest(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(filepath.Join(appDir, "version.json"))
	if err != nil {
		http.Error(w, "no app published", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(data)
}

func handleAppApk(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(filepath.Join(appDir, "app.apk"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="filehub.apk"`)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "filehub.apk", st.ModTime(), f)
}
