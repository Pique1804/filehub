package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const duplicateReadLimit = int64(4 << 30)

type duplicatePath struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type fileDuplicateGroup struct {
	SHA256    string          `json:"sha256"`
	Size      int64           `json:"size"`
	Savings   int64           `json:"savings"`
	Copies    int             `json:"copies"`
	PathCount int             `json:"pathCount"`
	Paths     []duplicatePath `json:"paths"`
}

type fileDuplicateResult struct {
	Groups     []fileDuplicateGroup `json:"groups"`
	GroupCount int                  `json:"groupCount"`
	Savings    int64                `json:"savings"`
	Checked    int                  `json:"checked"`
	Partial    bool                 `json:"partial"`
}

type duplicateCopy struct {
	stat  syscall.Stat_t
	hash  string
	paths []duplicatePath
}

type duplicateReader struct {
	ctx  context.Context
	file *os.File
}

func (reader duplicateReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.file.Read(buffer)
}

func sameDuplicateStat(a, b *syscall.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Size == b.Size && a.Mode == b.Mode &&
		a.Mtim == b.Mtim && a.Ctim == b.Ctim && a.Nlink == b.Nlink && a.Blocks == b.Blocks
}

func duplicateHash(ctx context.Context, file *os.File, before *syscall.Stat_t, buffer []byte) (string, bool) {
	hash := sha256.New()
	read, err := io.CopyBuffer(hash, io.LimitReader(duplicateReader{ctx, file}, before.Size+1), buffer)
	if err != nil || read != before.Size {
		return "", false
	}
	after, err := file.Stat()
	if err != nil {
		return "", false
	}
	stat, _ := storageFileStat(after)
	if !sameDuplicateStat(before, stat) {
		return "", false
	}
	return hex.EncodeToString(hash.Sum(nil)), true
}

func openIndexedDuplicate(root *os.Root, item duplicatePath, size int64) (*os.File, *syscall.Stat_t, storageInode) {
	full := filepath.Join(root.Name(), item.Path)
	if !publicPathInside(root.Name(), full) {
		return nil, nil, storageInode{}
	}
	info, err := root.Lstat(item.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return nil, nil, storageInode{}
	}
	file, err := root.OpenFile(item.Path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, storageInode{}
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() != size {
		file.Close()
		return nil, nil, storageInode{}
	}
	stat, id := storageFileStat(opened)
	return file, stat, id
}

func summarizeDuplicateCopies(hash string, size int64, copies []*duplicateCopy) fileDuplicateGroup {
	group := fileDuplicateGroup{SHA256: hash, Size: size, Copies: len(copies)}
	// Count each inode once. Any file with multiple hard links may have a name
	// outside this scan. Keep it out of the removable bytes estimate.
	smallest := int64(-1)
	retained := false
	for _, copy := range copies {
		group.Paths = append(group.Paths, copy.paths...)
		if copy.stat.Nlink != 1 {
			retained = true
			continue
		}
		allocated := copy.stat.Blocks * 512
		group.Savings += allocated
		if smallest == -1 || allocated < smallest {
			smallest = allocated
		}
	}
	if !retained && smallest >= 0 {
		group.Savings -= smallest
	}
	slices.SortFunc(group.Paths, func(a, b duplicatePath) int {
		if a.Source != b.Source {
			return strings.Compare(a.Source, b.Source)
		}
		return strings.Compare(a.Path, b.Path)
	})
	group.PathCount = len(group.Paths)
	group.Paths = group.Paths[:min(20, len(group.Paths))]
	return group
}

func scanIndexedDuplicates(ctx context.Context, source, folder string) (fileDuplicateResult, error) {
	result := fileDuplicateResult{Groups: []fileDuplicateGroup{}}
	bySize := map[int64][]duplicatePath{}
	partial, err := eachIndexedFile(ctx, source, folder, func(src, path string, size int64) {
		if size > 0 {
			bySize[size] = append(bySize[size], duplicatePath{Source: src, Path: path})
		}
	})
	result.Partial = partial
	if err != nil {
		return result, err
	}
	sizes := make([]int64, 0, len(bySize))
	for size, paths := range bySize {
		if len(paths) > 1 {
			sizes = append(sizes, size)
		}
	}
	slices.Sort(sizes)
	slices.Reverse(sizes)
	left := duplicateReadLimit
	buffer := make([]byte, 128<<10)
	roots := map[string]*os.Root{}
	defer func() {
		for _, root := range roots {
			if root != nil {
				root.Close()
			}
		}
	}()
	for _, size := range sizes {
		if ctx.Err() != nil {
			result.Partial = true
			break
		}
		inodes := map[storageInode]*duplicateCopy{}
		seenPaths := map[duplicatePath]bool{}
		for _, candidate := range bySize[size] {
			if ctx.Err() != nil {
				result.Partial = true
				break
			}
			if seenPaths[candidate] {
				continue
			}
			seenPaths[candidate] = true
			root, loaded := roots[candidate.Source]
			if !loaded {
				root, _ = os.OpenRoot(byName[candidate.Source])
				roots[candidate.Source] = root
			}
			if root == nil {
				result.Partial = true
				continue
			}
			file, stat, id := openIndexedDuplicate(root, candidate, size)
			if file == nil {
				result.Partial = true
				continue
			}
			if copy := inodes[id]; copy != nil && sameDuplicateStat(&copy.stat, stat) {
				copy.paths = append(copy.paths, candidate)
				file.Close()
				continue
			}
			delete(inodes, id)
			if size >= left {
				result.Partial = true
				file.Close()
				continue
			}
			left -= size + 1
			digest, valid := duplicateHash(ctx, file, stat, buffer)
			file.Close()
			if !valid {
				result.Partial = true
				continue
			}
			result.Checked++
			inodes[id] = &duplicateCopy{stat: *stat, hash: digest, paths: []duplicatePath{candidate}}
		}
		byHash := map[string][]*duplicateCopy{}
		for _, copy := range inodes {
			if ctx.Err() != nil {
				result.Partial = true
				break
			}
			// A name may have been moved, overwritten or relinked while its
			// other copies were being hashed. Check before showing it.
			currentPaths := copy.paths[:0]
			for _, item := range copy.paths {
				info, err := roots[item.Source].Lstat(item.Path)
				if err != nil || !info.Mode().IsRegular() {
					result.Partial = true
					continue
				}
				stat, _ := storageFileStat(info)
				if !sameDuplicateStat(&copy.stat, stat) || !publicPathInside(byName[item.Source], filepath.Join(byName[item.Source], item.Path)) {
					result.Partial = true
					continue
				}
				currentPaths = append(currentPaths, item)
			}
			if len(currentPaths) == 0 {
				continue
			}
			copy.paths = currentPaths
			byHash[copy.hash] = append(byHash[copy.hash], copy)
		}
		for digest, copies := range byHash {
			if len(copies) >= 2 {
				match := summarizeDuplicateCopies(digest, size, copies)
				result.Savings += match.Savings
				result.Groups = append(result.Groups, match)
			}
		}
	}
	slices.SortFunc(result.Groups, func(a, b fileDuplicateGroup) int {
		if a.Savings != b.Savings {
			if a.Savings > b.Savings {
				return -1
			}
			return 1
		}
		return strings.Compare(a.SHA256, b.SHA256)
	})
	result.GroupCount = len(result.Groups)
	result.Groups = result.Groups[:min(50, len(result.Groups))]
	return result, nil
}

func handleFileDuplicateScan(w http.ResponseWriter, r *http.Request) {
	release, ok := startStorageScan(w)
	if !ok {
		return
	}
	defer release()
	source, folder, ok := storageScope(r.URL.Query(), true)
	if !ok {
		http.Error(w, "storage folder unavailable", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := scanIndexedDuplicates(ctx, source, folder)
	if r.Context().Err() != nil {
		return
	}
	if err != nil && ctx.Err() == nil {
		http.Error(w, "could not read storage index", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, result)
}
