package main

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Pin private storage for the complete operation. Never resolve its name again
// after validation: another filesystem client may replace that name with a link.
type internalDir struct {
	file *os.File
	root *os.Root
}

// A completed rename with failed durability must retain its recovery metadata.
type committedMoveError struct{ error }

func openInternalDir(source, name string, create bool) (*internalDir, error) {
	if name != trashName && name != ".filehub-versions" {
		return nil, os.ErrInvalid
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	defer sourceRoot.Close()
	parent, err := sourceRoot.Open(".")
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if create {
		if err = unix.Mkdirat(int(parent.Fd()), name, 0700); err != nil && err != unix.EEXIST {
			return nil, err
		}
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	r, err := os.OpenRoot("/proc/self/fd/" + strconv.Itoa(fd))
	if err != nil {
		f.Close()
		return nil, err
	}
	return &internalDir{file: f, root: r}, nil
}

func (d *internalDir) Close() { d.root.Close(); d.file.Close() }
func privateLeaf(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}
func (d *internalDir) open(name string, flags int, mode uint32) (*os.File, error) {
	if !privateLeaf(name) {
		return nil, os.ErrInvalid
	}
	fd, err := unix.Openat(int(d.file.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, err
	}
	return f, nil
}
func (d *internalDir) read(name string) ([]byte, error) {
	f, err := d.open(name, unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 65537))
}
func (d *internalDir) write(name string, data []byte) error {
	f, err := d.open(name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// Root constrains parent resolution; renameat2 pins both directory endpoints
// and preserves the no-overwrite contract, including during rollback.
func sourceParent(source, full string, create bool) (*os.File, string, error) {
	rel, err := filepath.Rel(source, full)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return nil, "", os.ErrInvalid
	}
	r, err := os.OpenRoot(source)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	parent := filepath.Dir(rel)
	if create {
		if err = r.MkdirAll(parent, 0755); err != nil {
			return nil, "", err
		}
	}
	dir, err := r.OpenRoot(parent)
	if err != nil {
		return nil, "", err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	return f, filepath.Base(rel), err
}
func (d *internalDir) moveFrom(source, full, name string) error {
	if !privateLeaf(name) {
		return os.ErrInvalid
	}
	parent, leaf, err := sourceParent(source, full, false)
	if err != nil {
		return err
	}
	defer parent.Close()
	return d.moveFromParent(parent, leaf, name)
}

func (d *internalDir) moveFromParent(parent *os.File, leaf, name string) error {
	if !privateLeaf(name) || !privateLeaf(leaf) {
		return os.ErrInvalid
	}
	if err := unix.Renameat2(int(parent.Fd()), leaf, int(d.file.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if err := d.file.Sync(); err != nil {
		return &committedMoveError{err}
	}
	if err := parent.Sync(); err != nil {
		return &committedMoveError{err}
	}
	return nil
}
func (d *internalDir) moveTo(source, name, full string) error {
	if !privateLeaf(name) {
		return os.ErrInvalid
	}
	parent, leaf, err := sourceParent(source, full, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	return d.moveToParent(parent, name, leaf)
}

func (d *internalDir) moveToParent(parent *os.File, name, leaf string) error {
	if !privateLeaf(name) || !privateLeaf(leaf) {
		return os.ErrInvalid
	}
	if err := unix.Renameat2(int(d.file.Fd()), name, int(parent.Fd()), leaf, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if err := d.file.Sync(); err != nil {
		return &committedMoveError{err}
	}
	if err := parent.Sync(); err != nil {
		return &committedMoveError{err}
	}
	return nil
}

// Open every directory without following links so a scoped restore cannot
// be redirected to a sibling folder while resolving its destination.
func openPublicDirectory(source, relative string) (*os.File, error) {
	rel, ok := profileRelative(relative)
	if !ok {
		return nil, os.ErrInvalid
	}
	dir, err := os.Open(source)
	if err != nil || rel == "" {
		return dir, err
	}
	for _, part := range strings.Split(rel, "/") {
		fd, err := unix.Openat(int(dir.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		dir.Close()
		if err != nil {
			return nil, err
		}
		dir = os.NewFile(uintptr(fd), part)
	}
	return dir, nil
}

// Archive traversal already excludes symlinks. Enforce the same policy during
// the actual open, including ancestors replaced after WalkDir inspected them.
func openArchiveFile(source, relative string) (*os.File, error) {
	if !filepath.IsLocal(relative) || protectedRelativePath(relative) {
		return nil, os.ErrInvalid
	}
	// Preserve ordinary contained aliases, then open their canonical spelling
	// without following links again. A later replacement cannot redirect the open.
	canonicalRoot, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, err
	}
	canonicalFile, err := filepath.EvalSymlinks(filepath.Join(source, relative))
	if err != nil {
		return nil, err
	}
	relative, err = filepath.Rel(canonicalRoot, canonicalFile)
	if err != nil || !filepath.IsLocal(relative) || protectedRelativePath(relative) {
		return nil, os.ErrInvalid
	}
	parent, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(filepath.Clean(relative), string(os.PathSeparator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		fd, e := unix.Openat(int(parent.Fd()), part, flags, 0)
		parent.Close()
		if e != nil {
			return nil, e
		}
		parent = os.NewFile(uintptr(fd), part)
	}
	info, err := parent.Stat()
	if err != nil || !info.Mode().IsRegular() {
		parent.Close()
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, err
	}
	return parent, nil
}
