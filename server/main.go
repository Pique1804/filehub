// filehub — minimal self-hosted file server (multi-source + upload).
// Pure stdlib: disk "sources", listing, ranged streaming, streaming upload, embedded web UI.
package main

import (
	"archive/zip"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func diskUsage(path string) (total, free int64) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) == nil {
		total = int64(st.Blocks) * int64(st.Bsize)
		free = int64(st.Bavail) * int64(st.Bsize)
	}
	return
}

//go:embed web
var webFS embed.FS

type Entry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Owner   string `json:"owner,omitempty"`
}

type Source struct {
	Name string
	Root string
}

var sources []Source
var byName = map[string]string{}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func maxUploadBytes() int64 {
	const fallback = int64(50 << 30) // 50 GiB; configurable for larger trusted uploads
	v, err := strconv.ParseInt(env("FILEHUB_MAX_UPLOAD_BYTES", ""), 10, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func parseSources(spec string) {
	for _, part := range strings.Split(spec, ",") {
		name, path, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			continue
		}
		name, path = strings.TrimSpace(name), strings.TrimSpace(path)
		abs, err := filepath.Abs(path)
		if err != nil || name == "" {
			continue
		}
		sources = append(sources, Source{Name: name, Root: abs})
		byName[name] = abs
	}
}

func safePath(source, rel string) (string, bool) {
	if protectedRelativePath(rel) {
		return "", false
	}
	root, ok := byName[source]
	if !ok {
		return "", false
	}
	clean := filepath.Clean("/" + strings.TrimPrefix(rel, "/"))
	full := filepath.Join(root, clean)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", false
	}
	// Lexical containment isn't enough: an in-tree symlink (e.g. planted via the
	// Samba export) can point outside the source and be followed by os.Open/
	// ServeFile. realInside re-checks the symlink-resolved path — the same guard
	// WebDAV already uses — so download/thumb/preview/zip and the public /s/ and
	// /a/ links can't escape the root. Non-existent leaves resolve via the parent,
	// so upload/mkdir targets still pass.
	if !publicPathInside(root, full) {
		return "", false
	}
	return full, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func handleSources(w http.ResponseWriter, r *http.Request) {
	type srcInfo struct {
		Name  string `json:"name"`
		Total int64  `json:"total"`
		Free  int64  `json:"free"`
	}
	out := make([]srcInfo, 0, len(sources))
	access := requestFolders(r.Context())
	for _, s := range sources {
		if !access.canBrowse(s.Name, "") {
			continue
		}
		var total, free int64
		if access == nil {
			total, free = diskUsage(s.Root)
		}
		out = append(out, srcInfo{s.Name, total, free})
	}
	writeJSON(w, map[string]any{"sources": out})
}

func handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source, rel := q.Get("source"), q.Get("path")
	access := requestFolders(r.Context())
	full, ok := access.browsePath(source, rel)
	if !ok {
		if access != nil {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.IsDir() {
		http.Error(w, "not a directory", http.StatusBadRequest)
		return
	}
	des, err := f.ReadDir(-1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	entries := make([]Entry, 0, len(des))
	var ownerKeys []ownerPath
	for _, de := range des {
		if !access.visibleEntry(source, joinRel(rel, de.Name())) {
			continue
		}
		if strings.HasPrefix(de.Name(), ".filehub-") { // hide trash / tus staging
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{Name: de.Name(), IsDir: de.IsDir(), Size: info.Size(), ModTime: info.ModTime().Unix()})
		if info.Mode().IsRegular() {
			ownerKeys = append(ownerKeys, ownerPath{source, joinRel(rel, de.Name())})
		}
	}
	_, realDirectory := realProfilePath(source, rel)
	owners := fileOwnerLabels(r.Context(), ownerKeys, realDirectory)
	for i := range entries {
		entries[i].Owner = owners[ownerPath{source, joinRel(rel, entries[i].Name)}]
	}
	desc := q.Get("order") == "desc"
	sortKey := q.Get("sort")
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.IsDir != b.IsDir { // folders always grouped first
			return a.IsDir
		}
		var less bool
		switch sortKey {
		case "size":
			less = a.Size < b.Size || (a.Size == b.Size && strings.ToLower(a.Name) < strings.ToLower(b.Name))
		case "mtime":
			less = a.ModTime < b.ModTime || (a.ModTime == b.ModTime && strings.ToLower(a.Name) < strings.ToLower(b.Name))
		default:
			less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if desc {
			return !less
		}
		return less
	})
	writeJSON(w, map[string]any{"source": q.Get("source"), "path": q.Get("path"), "entries": entries})
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	full, ok := safePath(q.Get("source"), q.Get("path"))
	if !ok {
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, "not a file", http.StatusBadRequest)
		return
	}
	originalHeaders(w, full, q.Get("inline") != "")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// handleUpload streams the request body into <source>/<path>/<X-Filename>.
// uniqueDest returns dest if free, else dest with " (N)" inserted before the
// extension until a non-existing name is found — so an upload never overwrites
// a different existing file.
func uniqueDest(dest string) string {
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}
	ext := filepath.Ext(dest)
	base := strings.TrimSuffix(dest, ext)
	for i := 1; i < 10000; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return dest
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	if !profileWriteAllowed(w, r, q.Get("source"), q.Get("path"), true) {
		return
	}
	name := r.Header.Get("X-Filename")
	if dec, err := url.QueryUnescape(name); err == nil {
		name = dec
	}
	name = filepath.Base(strings.TrimSpace(name))
	if badName(name) {
		http.Error(w, "bad filename", http.StatusBadRequest)
		return
	}
	dir, ok := safePath(q.Get("source"), q.Get("path"))
	if !ok {
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		http.Error(w, "target is not a directory", http.StatusBadRequest)
		return
	}
	baseDest := filepath.Join(dir, name)
	limit := maxUploadBytes()
	if r.ContentLength > limit {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}
	_, statErr := os.Lstat(baseDest)
	hasCollision := statErr == nil
	staged, reservation, err := receiveDirectUpload(w, r, dir, limit, hasCollision)
	if writeQuotaError(w, err) {
		return
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	defer reservation.release()
	n := staged.size
	if staged.path == "" {
		handleDirectUploadRetry(w, r, dir, name, staged)
		return
	}
	// Publish with O_EXCL semantics. Identical bytes mean an idempotent retry;
	// equal size alone never proves that an existing photo is the same photo.
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	tmpName := staged.path
	defer os.Remove(tmpName)
	reservation.releaseLocked()
	if !profileWriteAllowed(w, r, q.Get("source"), q.Get("path"), true) {
		return
	}
	dest := baseDest
	checkedQuota := false
	for attempts := 0; attempts < uploadNameAttempts; attempts++ {
		dest = filepath.Join(dir, uploadCandidateName(name, attempts))
		if !checkedQuota {
			if st, err := os.Lstat(dest); err == nil {
				if st.Mode().IsRegular() {
					if same, e := filesEqual(tmpName, dest); e == nil && same {
						writeJSON(w, map[string]any{"ok": true, "name": filepath.Base(dest), "size": n, "identical": true})
						return
					}
				}
				if attempts == uploadNameAttempts-1 {
					http.Error(w, "too many filename conflicts", http.StatusConflict)
					return
				}
				continue
			}
			if !checkRequestQuota(w, r, n) {
				return
			}
			checkedQuota = true
		}
		if err := os.Link(tmpName, dest); err == nil {
			break
		} else if !errors.Is(err, os.ErrExist) {
			http.Error(w, "upload publish failed", http.StatusInternalServerError)
			return
		}
		if st, err := os.Lstat(dest); err == nil && st.Mode().IsRegular() {
			if same, err := filesEqual(tmpName, dest); err == nil && same {
				writeJSON(w, map[string]any{"ok": true, "name": filepath.Base(dest), "size": n, "identical": true})
				return
			}
		}
		if attempts == uploadNameAttempts-1 {
			http.Error(w, "too many filename conflicts", http.StatusConflict)
			return
		}
	}
	name = filepath.Base(dest)
	rel := joinRel(q.Get("path"), name)
	if err := indexAddPath(q.Get("source"), dest, rel); err != nil {
		http.Error(w, "Файл сохранён; ошибка индекса: "+err.Error(), 503)
		return
	}
	if err := queueMedia(q.Get("source"), rel); err != nil {
		http.Error(w, "Файл сохранён; ошибка очереди: "+err.Error(), 503)
		return
	}

	writeJSON(w, map[string]any{"ok": true, "name": name, "size": n})
}

// precompressedExt are formats not worth re-deflating (store as-is to save CPU).
var precompressedExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true,
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true, ".webm": true,
	".mp3": true, ".aac": true, ".ogg": true, ".flac": true, ".m4a": true,
	".zip": true, ".gz": true, ".7z": true, ".rar": true, ".xz": true, ".bz2": true,
}

func isPrecompressed(name string) bool { return precompressedExt[strings.ToLower(filepath.Ext(name))] }

// addToZip writes the file or directory tree at `full` into zw, keeping paths
// relative to full's parent. Private entries are excluded at every depth.
func addToZip(zw *zip.Writer, root, full string) {
	addArchiveTree(zw, root, full, filepath.Base(full))
}

func addArchiveTree(zw *zip.Writer, root, full, zipName string) {
	if zipName == string(filepath.Separator) {
		zipName = "."
	}
	filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rp, e := filepath.Abs(p)
		if e != nil || (rp != root && !strings.HasPrefix(rp, root+string(os.PathSeparator))) {
			return nil
		}
		sourceRel, e := filepath.Rel(root, p)
		if e != nil || protectedRelativePath(sourceRel) || !publicPathInside(root, p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, e := filepath.Rel(full, p)
		if e != nil {
			return nil
		}
		name := filepath.ToSlash(filepath.Join(zipName, rel))
		info, e := d.Info()
		if e != nil {
			return nil
		}
		if d.IsDir() {
			if name != "." {
				zw.Create(name + "/")
			}
			return nil
		}
		if !info.Mode().IsRegular() { // skip symlinks/devices
			return nil
		}
		f, e := openArchiveFile(root, sourceRel)
		if e != nil {
			return nil
		}
		defer f.Close()
		info, e = f.Stat()
		if e != nil {
			return nil
		}
		fh, e := zip.FileInfoHeader(info)
		if e != nil {
			return nil
		}
		fh.Name, fh.Modified = name, info.ModTime()
		if isPrecompressed(name) {
			fh.Method = zip.Store
		} else {
			fh.Method = zip.Deflate
		}
		wr, e := zw.CreateHeader(fh)
		if e != nil {
			return nil
		}
		io.Copy(wr, f)
		return nil
	})
}

// handleZip streams a .zip of a folder (?path=) or selected paths (?p=&p=) to the client.
func handleZip(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	root, ok := byName[q.Get("source")]
	if !ok {
		http.Error(w, "bad source", http.StatusBadRequest)
		return
	}
	targets := q["p"]
	if len(targets) == 0 {
		targets = []string{q.Get("path")}
	}
	access := requestFolders(r.Context())
	if access != nil {
		for _, target := range targets {
			if _, ok := access.readPath(q.Get("source"), target); !ok {
				http.NotFound(w, r)
				return
			}
		}
	}
	zipName := "filehub.zip"
	if len(targets) == 1 {
		if b := filepath.Base(filepath.Clean("/" + targets[0])); b != "." && b != "/" && b != "" {
			zipName = b + ".zip"
		}
	}
	zipName = strings.NewReplacer("\"", "_", "\n", "_", "\r", "_").Replace(zipName)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+zipName+"\"")
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, t := range targets {
		full, ok := safePath(q.Get("source"), t)
		if !ok {
			continue
		}
		if _, err := os.Stat(full); err != nil {
			continue
		}
		addToZip(zw, root, full)
	}
}

func main() {
	parseSources(env("FILEHUB_SOURCES", "HDD2:/mnt/hdd2"))
	if len(sources) == 0 {
		log.Fatal("filehub: no sources (set FILEHUB_SOURCES=\"HDD1:/mnt/data,HDD2:/mnt/hdd2\")")
	}
	port := env("PORT", "8080")

	initAuth()

	mux := http.NewServeMux()
	// public
	mux.HandleFunc("/api/login", requireMutation(handleLogin))
	mux.HandleFunc("/api/logout", requireBrowseMutation(handleLogout))
	mux.HandleFunc("/api/auth", handleAuthStatus)
	// WebDAV — own HTTP Basic auth (clients don't send cookies), so NOT behind requireAuth
	mux.Handle("/dav/", davHandler())
	// protected API
	mux.HandleFunc("/api/text", requireReadablePath(handleText))
	mux.HandleFunc("/api/text/events", requireReadablePath(handleTextEvents))
	mux.HandleFunc("/api/text/edit", requireUploadMutation(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "document editing is disabled", http.StatusForbidden)
	}))
	mux.HandleFunc("GET /api/profile/storage", requireBrowse(handleProfileStorage))
	mux.HandleFunc("/api/sources", requireBrowse(handleSources))
	mux.HandleFunc("/api/list", requireBrowse(handleList))
	mux.HandleFunc("/api/download", requireReadablePath(handleDownload))
	mux.HandleFunc("/api/thumb", requireReadablePath(handleThumb))
	mux.HandleFunc("/api/preview", requireReadablePath(handlePreview))
	mux.HandleFunc("/api/zip", requireBrowse(handleZip))
	mux.HandleFunc("/api/info", requireReadablePath(handleInfo))
	mux.HandleFunc("/api/photos", requireBrowse(handlePhotos))
	mux.HandleFunc("/api/maps/config", requireBrowse(handleMapConfig))
	mux.HandleFunc("/api/qr", requireAuth(handleQR))
	mux.HandleFunc("/api/app/latest", requireBrowse(handleAppLatest))
	mux.HandleFunc("/api/app/apk", requireBrowse(handleAppApk))
	mux.HandleFunc("/api/password", requireBrowseMutation(handleSelfPassword))
	mux.HandleFunc("/api/upload", requireUploadMutation(handleUpload))
	mux.HandleFunc("/api/mkdir", requireUploadMutation(handleMkdir))
	mux.HandleFunc("/api/delete", requireUploadMutation(handleDelete))
	mux.HandleFunc("/api/rename", requireUploadMutation(handleRename))
	mux.HandleFunc("/api/move", requireUploadMutation(handleMove))
	mux.HandleFunc("/api/copy", requireUploadMutation(handleCopy))
	mux.HandleFunc("/api/trash/list", requireBrowse(handleTrashList))
	mux.HandleFunc("/api/trash/restore", requireUploadMutation(handleTrashRestore))
	mux.HandleFunc("/api/trash/delete", requireUploadMutation(handleTrashDelete))
	mux.HandleFunc("/api/trash/empty", requireUploadMutation(handleTrashEmpty))
	tusDir := env("FILEHUB_TUS_DIR", "/mnt/hdd2/.filehub-tus")
	if err := registerTUS(mux, tusDir); err != nil {
		log.Printf("filehub: TUS disabled: %v", err)
	} else {
		startPartialUploadCleanup(tusDir)
	}
	if err := applyPendingRestore(dbPath()); err != nil {
		if _, statErr := os.Stat(dbPath()); statErr != nil {
			log.Fatalf("filehub: restore failed and current database is unavailable: %v", err)
		}
		log.Printf("filehub: restore not applied, keeping current database: %v", err)
	}
	if err := initSearch(env("FILEHUB_DB", "/opt/filehub/index.db")); err != nil {
		log.Fatalf("filehub: db init failed (auth needs it): %v", err)
	} else {
		legacyBackups := tableExists("albums")
		if err := initMaintenance(); err != nil {
			log.Fatal(err)
		}
		go mediaWorker()
		go func() {
			for {
				time.Sleep(10 * time.Minute)
				contentSweep()
			}
		}()
		go mediaTypeSweep()
		mux.HandleFunc("/api/photos/token", requireBrowse(handlePhotosToken))
		mux.HandleFunc("/api/photos/delta", requireBrowseMutation(handlePhotosDelta))
		mux.HandleFunc("/api/backup/reconcile", requireBrowseMutation(handleBackupReconcile))
		mux.HandleFunc("/api/photo/versions", requireReadablePath(handleVersions))
		mux.HandleFunc("/api/photo/restore", requireWriteMutation(handleVersionRestore))
		mux.HandleFunc("/api/photos/duplicates", requireAuth(handleDuplicates))
		mux.HandleFunc("/api/photos/duplicates/scan", requireWriteMutation(handleDuplicatesScan))
		if err := initUsers(); err != nil {
			log.Fatalf("filehub: users init failed: %v", err)
		}
		if err := initFavorites(); err != nil {
			log.Printf("filehub: favorites init: %v", err)
		}
		if err := initPhotoMeta(); err != nil {
			log.Printf("filehub: photo_meta init: %v", err)
		}
		mux.HandleFunc("/api/fav/toggle", requireWriteMutation(handleFavToggle))
		mux.HandleFunc("/api/fav/list", requireAuth(handleFavList))
		mux.HandleFunc("/api/photos/onthisday", requireBrowse(handleOnThisDay))
		mux.HandleFunc("/api/photos/places", requireBrowse(handlePlaces))
		mux.HandleFunc("/api/recents", requireBrowse(handleRecents))
		if err := initAlbums(); err != nil {
			log.Printf("filehub: albums init: %v", err)
		}
		mux.HandleFunc("/api/photo/rotate", requireWriteMutation(handleRotate))
		mux.HandleFunc("/api/albums/list", requireBrowse(handleAlbumList))
		mux.HandleFunc("/api/albums/create", requireWriteMutation(handleAlbumCreate))
		mux.HandleFunc("/api/albums/delete", requireWriteMutation(handleAlbumDelete))
		mux.HandleFunc("/api/albums/add", requireWriteMutation(handleAlbumAdd))
		mux.HandleFunc("/api/albums/remove", requireWriteMutation(handleAlbumRemove))
		mux.HandleFunc("/api/albums/photos", requireAuth(handleAlbumPhotos))
		mux.HandleFunc("/api/albums/share", requireWriteMutation(handleAlbumShare))
		mux.HandleFunc("/api/albums/revoke-link", requireWriteMutation(handleAlbumRevokeLink))
		mux.HandleFunc("/api/photo/crop", requireWriteMutation(handleCrop))
		mux.HandleFunc("/a/{token}", handleAlbumPublic)
		mux.HandleFunc("/a/{token}/t", handleAlbumPublicThumb)
		mux.HandleFunc("/a/{token}/f", handleAlbumPublicFull)
		registerInvitations(mux)
		// admin-only user management
		mux.HandleFunc("/api/admin/users", requireAdmin(handleUsersList))
		mux.HandleFunc("POST /api/admin/file-owner", requireAdminMutation(handleSetFileOwner))
		mux.HandleFunc("GET /api/admin/user/folders", requireAdmin(handleProfileFolders))
		mux.HandleFunc("POST /api/admin/user/folders", requireAdminMutation(setProfileFolders))
		mux.HandleFunc("/api/admin/user/create", requireAdminMutation(handleUserCreate))
		mux.HandleFunc("/api/admin/user/delete", requireAdminMutation(handleUserDelete))
		mux.HandleFunc("/api/admin/user/password", requireAdminMutation(handleUserPassword))
		mux.HandleFunc("/api/admin/user/role", requireAdminMutation(handleUserRole))
		// admin DB backup/restore
		mux.HandleFunc("/api/admin/backup", requireAdmin(handleBackup))
		mux.HandleFunc("/api/admin/restore", requireAdminMutation(handleRestore))
		backups, err := initManagedBackup(legacyBackups)
		if err != nil {
			log.Fatalf("filehub: backup settings: %v", err)
		}
		mux.HandleFunc("/api/admin/backup/settings", requireAdmin(backups.settings))
		mux.HandleFunc("/api/admin/backup/run", requireAdminMutation(backups.run))
		mux.HandleFunc("/api/backup/health", requireAuth(backups.health))
		mux.HandleFunc("/api/backup/copies", requireAuthMutation(backups.checkCopies))
		go backups.schedule()
		if backups.legacyEnabled() {
			go backupSweep(backups.legacyEnabled)
		}
		if err := registerVisualSearch(mux); err != nil {
			log.Printf("filehub: visual search init: %v", err)
		}

		// ephemeral streaming links for external/in-app video players
		mux.HandleFunc("/api/stream", requireReadablePath(handleStreamToken))
		mux.HandleFunc("/play/{token}", handleStreamPlay) // public (signed token)
		if err := registerVideoLibrary(mux); err != nil {
			log.Fatalf("filehub: video library init: %v", err)
		}

		mux.HandleFunc("/api/search", requireBrowse(handleSearch))
		registerStorageInsights(mux, tusDir)
		mux.HandleFunc("/api/reindex", requireWriteMutation(handleReindex))
		if err := initContent(); err != nil {
			log.Printf("filehub: content index init: %v", err)
		}
		mux.HandleFunc("/api/search/content", requireBrowse(handleContentSearch))
		mux.HandleFunc("/api/search/photos/text", requireBrowse(handlePhotoTextSearch))
		mux.HandleFunc("/api/content/reindex", requireWriteMutation(handleContentReindex))
		mux.HandleFunc("/api/content/status", requireAuth(handleContentStatus))
		if err := initShares(); err != nil {
			log.Printf("filehub: shares disabled: %v", err)
		} else {
			registerShares(mux)
		}
		needsInitialIndex := indexEmpty()
		go func() {
			if err := initRecentIndex(); err != nil {
				log.Printf("filehub: recent file index init: %v", err)
			}
			if needsInitialIndex {
				reindex()
			}
		}()
		// Pre-generate thumbnails for the whole library in the background so the
		// photo gallery scrolls instantly (skips already-cached; safe to re-run).
		go func() {
			time.Sleep(15 * time.Second) // let any reindex make progress first
			prewarmAll()
		}()
		go func() {
			time.Sleep(60 * time.Second)
			contentSweep()
		}()
	}
	// Purge trash entries older than 30 days, now and every 6 hours.
	go func() {
		for {
			sweepTrash()
			time.Sleep(6 * time.Hour)
		}
	}()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", webReferrerPolicy(embeddedWebAssets(sub)))

	parts := make([]string, len(sources))
	for i, s := range sources {
		parts[i] = s.Name + "=" + s.Root
	}
	log.Printf("filehub: sources [%s] on :%s", strings.Join(parts, " "), port)
	server := &http.Server{
		Addr:              net.JoinHostPort(os.Getenv("FILEHUB_BIND_HOST"), port),
		Handler:           securityMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	log.Fatal(server.ListenAndServe())
}
