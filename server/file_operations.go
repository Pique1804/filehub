package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Caller holds fileMutationMu. API and WebDAV use the same filesystem/metadata
// transaction, including a compensating move if SQLite rejects the remap.
func moveFileReferences(source, old, destSource, dest string) error {
	from, root, ok := resolveItem(source, old)
	to, destRoot, destOK := resolveItem(destSource, dest)
	if !ok || !destOK || from == root || to == destRoot {
		return os.ErrInvalid
	}
	if from == to {
		return nil
	}
	if strings.HasPrefix(to, from+string(os.PathSeparator)) {
		return os.ErrInvalid
	}
	owners, err := captureFileOwnerMove(source, old)
	if err != nil {
		return err
	}
	if err := moveFileNoReplace(from, to); err != nil {
		return err
	}
	if err := remapFileReferences(source, old, destSource, dest, owners...); err != nil {
		return errors.Join(err, moveFileNoReplace(to, from))
	}
	return nil
}

func deleteFileToTrash(source, rel string) error {
	_, err := deleteFileToTrashRecord(source, rel)
	return err
}

func deleteFileToTrashRecord(source, rel string) (trashMeta, error) {
	full, root, ok := resolveItem(source, rel)
	if !ok || full == root {
		return trashMeta{}, os.ErrInvalid
	}
	d, err := openInternalDir(root, trashName, true)
	if err != nil {
		return trashMeta{}, err
	}
	defer d.Close()
	owners, err := captureFileOwnerMove(source, rel)
	if err != nil {
		return trashMeta{}, err
	}
	meta, err := moveToTrashPinned(root, full, rel, d)
	if err != nil {
		return trashMeta{}, err
	}
	archived := trashName + "/" + meta.ID
	// Keep associated versions/favorites/shares with the deleted item. Moving a
	// different file into its former name must not inherit those references.
	if err = updateFileReferences(source, rel, source, archived, true, owners...); err != nil {
		rollback := d.moveTo(root, meta.ID, full)
		if rollback == nil {
			d.root.Remove(meta.ID + ".meta")
		}
		return trashMeta{}, errors.Join(err, rollback)
	}
	return meta, nil
}

func refreshReplacedFile(source, rel, full string) error {
	if err := indexAddPath(source, full, rel); err != nil {
		return err
	}
	if db == nil {
		return nil
	}
	if _, err := db.Exec(`INSERT INTO media_revision VALUES(?,?,?) ON CONFLICT(source,path) DO UPDATE SET revision=excluded.revision`, source, rel, fmt.Sprint(time.Now().UnixNano())); err != nil {
		return err
	}
	for _, table := range []string{"file_hashes", "photo_embed", "photo_meta"} {
		if tableExists(table) {
			if _, err := db.Exec(`DELETE FROM `+table+` WHERE source=? AND path=?`, source, rel); err != nil {
				return err
			}
		}
	}
	contentDelete(source, rel)
	invalidateMediaCaches()
	return queueMedia(source, rel)
}
