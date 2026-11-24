package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const storageIndexLimit = 100_000
const storageWalkLimit = 100_000

var storageScanSlot = make(chan struct{}, 1)

func registerStorageInsights(mux *http.ServeMux, uploadDir string) {
	mux.HandleFunc("GET /api/admin/storage/usage", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		handleStorageUsage(w, r, uploadDir)
	}))
	mux.HandleFunc("GET /api/admin/storage/duplicates", requireAdmin(handleFileDuplicateScan))
}

func startStorageScan(w http.ResponseWriter) (release func(), ok bool) {
	w.Header().Set("Cache-Control", "no-store")
	select {
	case storageScanSlot <- struct{}{}:
		return func() { <-storageScanSlot }, true
	default:
		http.Error(w, "another storage scan is running", http.StatusTooManyRequests)
		return nil, false
	}
}

func storageScope(query url.Values, allowAll bool) (source, folder string, ok bool) {
	source, folder = query.Get("source"), query.Get("path")
	if source == "" {
		return "", "", allowAll && folder == ""
	}
	if folder != "" && !validIndexedPath(folder) {
		return "", "", false
	}
	full, ok := safePath(source, folder)
	if !ok {
		return "", "", false
	}
	info, err := os.Stat(full)
	return source, folder, err == nil && info.IsDir()
}

func validIndexedPath(relative string) bool {
	return len(relative) <= 4096 && relative != "." && filepath.IsLocal(relative) &&
		path.Clean(relative) == relative && !protectedRelativePath(relative)
}

func eachIndexedFile(ctx context.Context, source, folder string, visit func(string, string, int64)) (bool, error) {
	query := `SELECT source,path,size FROM files WHERE isdir=0`
	var args []any
	if source != "" {
		query += ` AND source=?`
		args = append(args, source)
	}
	if folder != "" {
		prefix := folder + "/"
		query += ` AND substr(path,1,?)=?`
		args = append(args, utf8.RuneCountInString(prefix), prefix)
	}
	rows, err := db.QueryContext(ctx, query+` LIMIT ?`, append(args, storageIndexLimit+1)...)
	if err != nil {
		return ctx.Err() != nil, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if count == storageIndexLimit {
			return true, nil
		}
		count++
		var src, relative string
		var size int64
		if err := rows.Scan(&src, &relative, &size); err != nil {
			return true, err
		}
		if validIndexedPath(relative) && size >= 0 {
			visit(src, relative, size)
		}
	}
	return ctx.Err() != nil, rows.Err()
}

type storageFolderUsage struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

func indexedStorageFolders(ctx context.Context, source, folder string) ([]storageFolderUsage, bool, error) {
	byFolder := map[string]storageFolderUsage{}
	var prefix string
	if folder != "" {
		prefix = folder + "/"
	}
	partial, err := eachIndexedFile(ctx, source, folder, func(_, relative string, size int64) {
		base, _, isChild := strings.Cut(strings.TrimPrefix(relative, prefix), "/")
		if !isChild {
			return
		}
		key := prefix + base
		item := byFolder[key]
		item.Path = key
		item.Bytes += size
		item.Files++
		byFolder[key] = item
	})
	out := make([]storageFolderUsage, 0, len(byFolder))
	for _, item := range byFolder {
		out = append(out, item)
	}
	slices.SortFunc(out, func(a, b storageFolderUsage) int {
		if a.Bytes != b.Bytes {
			if a.Bytes > b.Bytes {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out[:min(20, len(out))], partial, err
}

type storageVolumeUsage struct {
	Sources  []string `json:"sources"`
	Total    int64    `json:"total"`
	Free     int64    `json:"free"`
	Trash    int64    `json:"trash"`
	Versions int64    `json:"versions"`
	Previews int64    `json:"previews"`
	Uploads  int64    `json:"uploads"`
}

type storageInode struct{ dev, ino uint64 }

func storageFileStat(info os.FileInfo) (*syscall.Stat_t, storageInode) {
	stat := info.Sys().(*syscall.Stat_t)
	return stat, storageInode{uint64(stat.Dev), uint64(stat.Ino)}
}

type storageUsageScan struct {
	ctx     context.Context
	seen    map[storageInode]bool
	devices map[uint64]*storageVolumeUsage
	left    int
	partial bool
}

func (scan *storageUsageScan) volume(dev uint64) *storageVolumeUsage {
	if scan.devices[dev] == nil {
		scan.devices[dev] = &storageVolumeUsage{Sources: []string{}}
	}
	return scan.devices[dev]
}

func (scan *storageUsageScan) takeEntry() bool {
	if scan.left == 0 || scan.ctx.Err() != nil {
		scan.partial = true
		return false
	}
	scan.left--
	return true
}

func (scan *storageUsageScan) add(root *os.Root, file, category string) {
	info, err := root.Lstat(file)
	if err != nil {
		scan.partial = true
		return
	}
	if !info.Mode().IsRegular() {
		return
	}
	stat, identity := storageFileStat(info)
	if scan.seen[identity] {
		return
	}
	scan.seen[identity] = true
	volume := scan.volume(identity.dev)
	allocated := stat.Blocks * 512
	switch category {
	case "trash":
		volume.Trash += allocated
	case "versions":
		volume.Versions += allocated
	case "previews":
		volume.Previews += allocated
	case "uploads":
		volume.Uploads += allocated
	}
}

func (scan *storageUsageScan) walk(root *os.Root, folder, category string, depth int) {
	if depth >= 128 || scan.ctx.Err() != nil {
		scan.partial = true
		return
	}
	dir, err := root.OpenFile(folder, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		scan.partial = true
		return
	}
	defer dir.Close()
	for {
		entries, err := dir.ReadDir(128)
		for _, entry := range entries {
			if !scan.takeEntry() {
				return
			}
			file := filepath.Join(folder, entry.Name())
			if entry.IsDir() {
				scan.walk(root, file, category, depth+1)
			} else if entry.Type().IsRegular() {
				scan.add(root, file, category)
			}
		}
		if err != nil {
			scan.partial = scan.partial || !errors.Is(err, io.EOF)
			return
		}
	}
}

func (scan *storageUsageScan) configuredDir(directory string, measure func(*os.Root)) {
	if scan.ctx.Err() != nil {
		scan.partial = true
		return
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		scan.partial = scan.partial || !errors.Is(err, fs.ErrNotExist)
		return
	}
	defer root.Close()
	if info, err := root.Stat("."); err == nil {
		_, id := storageFileStat(info)
		volume := scan.volume(id.dev)
		if volume.Total == 0 {
			volume.Total, volume.Free = diskUsage(directory)
		}
	}
	measure(root)
}

func (scan *storageUsageScan) incompleteUploads(directory string) {
	scan.configuredDir(directory, func(root *os.Root) {
		dir, err := root.Open(".")
		if err != nil {
			scan.partial = true
			return
		}
		defer dir.Close()
		for {
			entries, err := dir.ReadDir(128)
			for _, entry := range entries {
				if !scan.takeEntry() {
					return
				}
				id, found := strings.CutSuffix(entry.Name(), ".info")
				if !found {
					continue
				}
				if _, ok := readPartialUpload(directory, id); ok {
					scan.add(root, id, "uploads")
					scan.add(root, entry.Name(), "uploads")
				}
			}
			if err != nil {
				scan.partial = scan.partial || !errors.Is(err, io.EOF)
				return
			}
		}
	})
}

func storageVolumeBreakdown(ctx context.Context, uploadDir string) ([]storageVolumeUsage, bool) {
	scan := storageUsageScan{ctx: ctx, seen: map[storageInode]bool{}, devices: map[uint64]*storageVolumeUsage{}, left: storageWalkLimit}
	scanned := map[storageInode]bool{}
	for _, source := range sources {
		if ctx.Err() != nil {
			scan.partial = true
			break
		}
		info, err := os.Stat(source.Root)
		if err != nil || !info.IsDir() {
			scan.partial = true
			continue
		}
		_, id := storageFileStat(info)
		volume := scan.volume(id.dev)
		volume.Sources = append(volume.Sources, source.Name)
		if volume.Total == 0 {
			volume.Total, volume.Free = diskUsage(source.Root)
			scan.partial = scan.partial || volume.Total == 0
		}
		if scanned[id] {
			continue
		}
		scanned[id] = true
		for _, category := range []struct{ name, kind string }{{trashName, "trash"}, {".filehub-versions", "versions"}} {
			dir, err := openInternalDir(source.Root, category.name, false)
			if err == nil {
				scan.walk(dir.root, ".", category.kind, 0)
				dir.Close()
			} else if !errors.Is(err, fs.ErrNotExist) {
				scan.partial = true
			}
		}
	}
	scan.incompleteUploads(uploadDir)
	for _, directory := range []string{previewDir, thumbDir} {
		scan.configuredDir(directory, func(root *os.Root) { scan.walk(root, ".", "previews", 0) })
	}
	keys := make([]uint64, 0, len(scan.devices))
	for id := range scan.devices {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	out := make([]storageVolumeUsage, 0, len(keys))
	for _, id := range keys {
		volume := scan.devices[id]
		slices.Sort(volume.Sources)
		out = append(out, *volume)
	}
	return out, scan.partial
}

func handleStorageUsage(w http.ResponseWriter, r *http.Request, uploadDir string) {
	release, ok := startStorageScan(w)
	if !ok {
		return
	}
	defer release()
	params := r.URL.Query()
	source, folder, ok := storageScope(params, false)
	if !ok {
		http.Error(w, "storage folder unavailable", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	folders, folderPartial, err := indexedStorageFolders(ctx, source, folder)
	if err != nil && ctx.Err() == nil {
		http.Error(w, "could not read storage index", http.StatusServiceUnavailable)
		return
	}
	result := map[string]any{"folders": folders, "folderPartial": folderPartial}
	if params.Get("foldersOnly") != "1" {
		volumes, volumePartial := storageVolumeBreakdown(ctx, uploadDir)
		result["volumes"] = volumes
		result["volumePartial"] = volumePartial
	}
	if r.Context().Err() != nil {
		return
	}
	writeJSON(w, result)
}
