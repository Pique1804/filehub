package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type ownerPath struct{ source, path string }
type ownerBinding struct{ account, identity string }

const fileOwnerRange = `source=? AND (path=? OR (path>=? AND path<?))`
const putFileOwner = `INSERT INTO file_owners(source,path,owner_id,object_id) VALUES(?,?,?,?)
	ON CONFLICT(source,path) DO UPDATE SET owner_id=excluded.owner_id,object_id=excluded.object_id`

func personalFolderOwners(ctx context.Context) (map[folderGrant]string, error) {
	results := make(map[folderGrant]string)
	rows, err := db.QueryContext(ctx, `SELECT username,folder_access FROM users WHERE folder_access IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		access, err := decodeFolderAccess(raw)
		if err == nil && access.Personal != nil {
			if _, exists := results[*access.Personal]; exists {
				// An ambiguous legacy policy cannot establish ownership.
				results[*access.Personal] = ""
			} else {
				results[*access.Personal] = name
			}
		}
	}
	return results, rows.Err()
}

func inheritedOwner(key ownerPath, personal map[folderGrant]string) (string, bool) {
	var name string
	found := false
	for folder, owner := range personal {
		if folder.Source == key.source && pathWithin(key.path, folder.Path) {
			if found {
				return "", true
			}
			name, found = owner, true
		}
	}
	return name, found
}

// Open the regular file inside its pinned parent without following a symlink.
// The internal variant is used only to transfer a known record through trash.
func ownerObject(key ownerPath, internal bool) (string, error) {
	base := byName[key.source]
	if base == "" || !filepath.IsLocal(key.path) || key.path == "." {
		return "", os.ErrInvalid
	}
	var dir *os.Root
	leaf := key.path
	if internal && strings.HasPrefix(key.path, trashName+"/") {
		trash, err := openInternalDir(base, trashName, false)
		if err != nil {
			return "", err
		}
		defer trash.Close()
		dir, leaf = trash.root, strings.TrimPrefix(key.path, trashName+"/")
	} else {
		if rel, ok := profileRelative(key.path); !ok || rel != key.path {
			return "", os.ErrNotExist
		}
		parent, err := openPublicDirectory(base, parentRel(key.path))
		if err != nil {
			return "", err
		}
		defer parent.Close()
		fd, err := unix.Openat(int(parent.Fd()), filepath.Base(key.path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return "", err
		}
		f := os.NewFile(uintptr(fd), filepath.Base(key.path))
		defer f.Close()
		if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
			return "", os.ErrNotExist
		}
		return sharedObjectID(f)
	}
	before, err := dir.Lstat(leaf)
	if err != nil || !before.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	file, err := dir.OpenFile(leaf, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return sharedObjectID(file)
}

// Callers pass only files already visible to the requester. A batch lookup
// keeps search and recent files independent of the size of the ownership table.
func fileOwnerLabels(ctx context.Context, keys []ownerPath, regularlyListed bool) map[ownerPath]string {
	if db == nil || len(keys) == 0 {
		return nil
	}
	personal, err := personalFolderOwners(ctx)
	if err != nil {
		return nil
	}
	labels := make(map[ownerPath]string)
	var shared []ownerPath
	for _, key := range keys {
		if owner, within := inheritedOwner(key, personal); within {
			if regularlyListed {
				labels[key] = owner
			} else if _, err := ownerObject(key, false); err == nil {
				labels[key] = owner
			}
		} else {
			shared = append(shared, key)
		}
	}
	for len(shared) != 0 {
		n := min(len(shared), 200)
		loadExplicitFileOwners(ctx, shared[:n], labels)
		shared = shared[n:]
	}
	return labels
}

func loadExplicitFileOwners(ctx context.Context, batch []ownerPath, labels map[ownerPath]string) {
	args := make([]any, 0, 2*len(batch))
	for _, key := range batch {
		args = append(args, key.source, key.path)
	}
	rows, err := db.QueryContext(ctx, `WITH wanted(source,path) AS (VALUES `+strings.TrimSuffix(strings.Repeat("(?,?),", len(batch)), ",")+`)
		SELECT f.source,f.path,f.object_id,u.username FROM wanted w JOIN file_owners f
		ON f.source=w.source AND f.path=w.path JOIN users u ON u.account_id=f.owner_id`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key ownerPath
		var expected, owner string
		if rows.Scan(&key.source, &key.path, &expected, &owner) == nil {
			if actual, err := ownerObject(key, false); err == nil && actual == expected {
				labels[key] = owner
			}
		}
	}
}

func fileOwnerToken(identity string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
}

func handleSetFileOwner(w http.ResponseWriter, r *http.Request) {
	var edit struct{ Source, Path, Owner, Token string }
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSmallFormBytes))
	d.DisallowUnknownFields()
	if d.Decode(&edit) != nil || d.Decode(new(any)) != io.EOF || len(edit.Token) != 64 ||
		(edit.Owner != "" && !validUsername(edit.Owner)) {
		http.Error(w, "invalid owner assignment", http.StatusBadRequest)
		return
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	key := ownerPath{edit.Source, edit.Path}
	identity, err := ownerObject(key, false)
	if err != nil || fileOwnerToken(identity) != edit.Token {
		http.Error(w, "file changed", http.StatusConflict)
		return
	}
	personal, err := personalFolderOwners(r.Context())
	if err != nil {
		http.Error(w, "ownership unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, within := inheritedOwner(key, personal); within {
		http.Error(w, "ownership follows the personal folder", http.StatusConflict)
		return
	}
	if edit.Owner == "" {
		_, err = db.ExecContext(r.Context(), `DELETE FROM file_owners WHERE source=? AND path=?`, key.source, key.path)
	} else {
		var id string
		err = db.QueryRowContext(r.Context(), `SELECT account_id FROM users WHERE username=? AND account_id!=''`, edit.Owner).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "profile unavailable", http.StatusNotFound)
			return
		}
		if err == nil {
			_, err = db.ExecContext(r.Context(), putFileOwner, key.source, key.path, id, identity)
		}
	}
	if err != nil {
		http.Error(w, "could not save owner", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type fileOwnerMove struct{ suffix, account string }

// Capture only still-valid bindings before a trusted move. A copy across disks
// receives a new inode, but inherits the owner when FileHub moved the original.
func captureFileOwnerMove(source, path string) ([]fileOwnerMove, error) {
	if !tableExists("file_owners") {
		return nil, nil
	}
	rows, err := db.Query(`SELECT path,owner_id,object_id FROM file_owners WHERE `+fileOwnerRange, source, path, path+"/", path+"0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var moves []fileOwnerMove
	for rows.Next() {
		var p string
		var binding ownerBinding
		if err := rows.Scan(&p, &binding.account, &binding.identity); err != nil {
			return nil, err
		}
		if identity, err := ownerObject(ownerPath{source, p}, true); err == nil && identity == binding.identity {
			moves = append(moves, fileOwnerMove{strings.TrimPrefix(p, path), binding.account})
		}
	}
	return moves, rows.Err()
}

func deleteFileOwnerTree(execer interface {
	Exec(string, ...any) (sql.Result, error)
}, source, path string) error {
	_, err := execer.Exec(`DELETE FROM file_owners WHERE `+fileOwnerRange, source, path, path+"/", path+"0")
	return err
}

func relocateFileOwners(tx *sql.Tx, source, old, destSource, dest string, moves []fileOwnerMove) error {
	if err := deleteFileOwnerTree(tx, source, old); err != nil {
		return err
	}
	if err := deleteFileOwnerTree(tx, destSource, dest); err != nil {
		return err
	}
	for _, move := range moves {
		key := ownerPath{destSource, dest + move.suffix}
		identity, err := ownerObject(key, true)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(putFileOwner, key.source, key.path, move.account, identity); err != nil {
			return err
		}
	}
	return nil
}
