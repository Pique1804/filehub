package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

type davReplacementPath struct{ source, path string }

type davReplacement struct {
	client, trashID string
	expires         time.Time
	placeholder     os.FileInfo
}

const finderReplacementWindow = 2 * time.Minute

// Finder implements Replace as DELETE, an empty PUT, and a separate data PUT.
// Identify only that short sequence, per authenticated WebDAV client. Trash
// remains the durable recovery path if the data PUT never reaches FileHub.
// Access to replacements is serialized by fileMutationMu.
func (fs davFS) rememberFinderDeletion(key davReplacementPath, client, id string) {
	if fs.replacements == nil {
		return
	}
	now := time.Now()
	if len(fs.replacements) >= 1024 {
		for k, entry := range fs.replacements {
			if now.After(entry.expires) {
				delete(fs.replacements, k)
			}
		}
		if len(fs.replacements) >= 1024 {
			return
		}
	}
	fs.replacements[key] = &davReplacement{client: client, trashID: id, expires: now.Add(finderReplacementWindow)}
}

func (fs davFS) finderReplacement(client, source, path string) *davReplacement {
	key := davReplacementPath{source, path}
	entry := fs.replacements[key]
	if entry == nil || entry.client != client {
		return nil
	}
	if time.Now().After(entry.expires) {
		delete(fs.replacements, key)
		return nil
	}
	return entry
}

func sameFinderPlaceholder(expected, actual os.FileInfo) bool {
	return expected != nil && actual != nil && actual.Mode().IsRegular() && actual.Size() == 0 &&
		os.SameFile(expected, actual) && expected.ModTime().Equal(actual.ModTime())
}

func (f *davFile) observeFinderPublication(old os.FileInfo, missing bool) {
	key := davReplacementPath{f.source, f.rel}
	entry := f.fs.replacements[key]
	if entry == nil {
		return
	}
	if entry == f.replacement && f.input.received == 0 &&
		((entry.placeholder == nil && missing) || sameFinderPlaceholder(entry.placeholder, old)) {
		if current, err := os.Lstat(f.full); err == nil && current.Mode().IsRegular() && current.Size() == 0 {
			entry.placeholder = current
			return
		}
	}
	delete(f.fs.replacements, key)
}

func (f *davFile) recoverFinderReplacementLocked() {
	key := davReplacementPath{f.source, f.rel}
	entry := f.replacement
	if entry == nil || f.fs.replacements[key] != entry || entry.placeholder == nil ||
		(f.input.expected <= 0 && f.input.received == 0) {
		return
	}
	account, ok := f.ctx.Value(profileKey{}).(profileIdentity)
	if !ok {
		return
	}
	currentAccount, err := loadProfile(account.username)
	if err != nil || currentAccount.version != account.version || currentAccount.role == "viewer" ||
		!currentAccount.folders.canWrite(f.source, f.rel, false) {
		return
	}
	current, err := os.Lstat(f.full)
	if err != nil || !sameFinderPlaceholder(entry.placeholder, current) {
		return
	}
	delete(f.fs.replacements, key)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 10*time.Second)
	defer cancel()
	if err := restoreFinderReplacement(ctx, currentAccount, f.source, f.rel, entry); err != nil {
		log.Printf("webdav: could not restore interrupted Finder replacement: %v", err)
	}
}

func restoreFinderReplacement(ctx context.Context, account profileIdentity, source, rel string, entry *davReplacement) error {
	root := byName[source]
	d, err := openInternalDir(root, trashName, false)
	if err != nil {
		return err
	}
	defer d.Close()
	if !validTrashID(entry.trashID) {
		return os.ErrInvalid
	}
	var original trashMeta
	metadata, err := d.read(entry.trashID + ".meta")
	if err != nil || json.Unmarshal(metadata, &original) != nil || original.ID != entry.trashID || original.OrigPath != rel || original.IsDir {
		return os.ErrNotExist
	}
	if info, err := d.root.Lstat(original.ID); err != nil || !info.Mode().IsRegular() || info.Size() != original.Size {
		return os.ErrNotExist
	}
	if err := checkQuotaGrowth(ctx, account, original.Size, ""); err != nil {
		return err
	}
	parent, err := openPublicDirectory(root, parentRel(rel))
	if err != nil {
		return err
	}
	defer parent.Close()
	leaf := filepath.Base(rel)
	placeholder, err := deleteFileToTrashRecord(source, rel)
	if err != nil {
		return err
	}
	archived, err := d.root.Lstat(placeholder.ID)
	if err != nil || !sameFinderPlaceholder(entry.placeholder, archived) {
		return errors.Join(os.ErrInvalid, restoreFinderTrashFile(d, parent, source, rel, leaf, placeholder.ID))
	}
	if err := restoreFinderTrashFile(d, parent, source, rel, leaf, original.ID); err != nil {
		return errors.Join(err, restoreFinderTrashFile(d, parent, source, rel, leaf, placeholder.ID))
	}
	return purgeTrashItem(d, source, placeholder.ID)
}

func restoreFinderTrashFile(d *internalDir, parent *os.File, source, rel, leaf, id string) error {
	owners, err := captureFileOwnerMove(source, trashName+"/"+id)
	if err != nil {
		return err
	}
	if err := d.moveToParent(parent, id, leaf); err != nil {
		return err
	}
	if err := remapFileReferences(source, trashName+"/"+id, source, rel, owners...); err != nil {
		return errors.Join(err, d.moveFromParent(parent, leaf, id))
	}
	return errors.Join(d.root.Remove(id+".meta"), indexAddPath(source, filepath.Join(byName[source], filepath.FromSlash(rel)), rel))
}
