package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// filesEqual only considers regular files. A destination symlink must never
// be followed while resolving an upload collision.
func filesEqual(a, b string) (bool, error) {
	as, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	bs, err := os.Lstat(b)
	if err != nil {
		return false, err
	}
	if !as.Mode().IsRegular() || !bs.Mode().IsRegular() || as.Size() != bs.Size() {
		return false, nil
	}
	af, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer af.Close()
	bf, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer bf.Close()
	ab, bb := make([]byte, 128<<10), make([]byte, 128<<10)
	for {
		an, ae := af.Read(ab)
		bn, be := bf.Read(bb)
		if an != bn || !bytes.Equal(ab[:an], bb[:bn]) {
			return false, nil
		}
		if ae == io.EOF && be == io.EOF {
			return true, nil
		}
		if ae != nil && ae != io.EOF {
			return false, ae
		}
		if be != nil && be != io.EOF {
			return false, be
		}
	}
}

// renameNoReplace is an atomic move that cannot replace a destination created
// by another Filehub request or by a client of the same storage volume.
func renameNoReplace(src, dst string) error {
	if src == dst {
		return nil
	}
	return unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
}

// moveFileNoReplace also supports a regular file moving between disks. The
// destination is published with a hard link only after the copy is complete.
func moveFileNoReplace(src, dst string) error {
	if err := renameNoReplace(src, dst); err != unix.EXDEV {
		return err
	}
	// Stage the complete tree on the destination filesystem. Never remove
	// individual source files while a directory copy is still in progress.
	if err := filepath.WalkDir(src, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return os.ErrInvalid
		}
		return nil
	}); err != nil {
		return err
	}
	if err := copyPath(src, dst); err != nil {
		return err
	}
	// copyPath publishes only after every file and directory has been synced.
	if err := syncFile(filepath.Dir(dst)); err != nil {
		return err
	}
	if err := os.RemoveAll(src); err != nil {
		return err
	}
	return syncFile(filepath.Dir(src))
}
