package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type folderGrant struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

// A NULL policy keeps the historical shared access. A non-NULL empty list
// grants nothing. Limited profiles are readers even if their configured
// role is "user" unless the administrator assigns a personal folder.
type folderAccess struct {
	Folders    []folderGrant `json:"folders"`
	Personal   *folderGrant  `json:"personal,omitempty"`
	QuotaBytes int64         `json:"quotaBytes,omitempty"`
}

func (access *folderAccess) grants() []folderGrant {
	if access.Personal == nil {
		return access.Folders
	}
	return append(append([]folderGrant{}, access.Folders...), *access.Personal)
}

func (access *folderAccess) uploadDirectory(source, rel string) (string, bool) {
	if access == nil {
		return safePath(source, rel)
	}
	path, ok := profileRelative(rel)
	if !ok || !access.canWrite(source, path, true) {
		return "", false
	}
	return realProfilePath(source, path)
}

func (access *folderAccess) canWrite(source, rel string, includeRoot bool) bool {
	if access == nil {
		return true
	}
	path, ok := profileRelative(rel)
	return ok && access.Personal != nil && access.Personal.Source == source &&
		(includeRoot || access.Personal.Path != path) && pathWithin(path, access.Personal.Path)
}

const maxFolderGrants = 64

func initFolderAccess() error {
	var found int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('users') WHERE name='folder_access'").Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		_, err := db.Exec("ALTER TABLE users ADD COLUMN folder_access TEXT")
		return err
	}
	return nil
}

type profileIdentity struct {
	username, role, version string
	folders                 *folderAccess
}

type profileKey struct{}

func loadProfile(username string) (profileIdentity, error) {
	account := profileIdentity{username: username}
	if db == nil {
		return account, errors.New("user database unavailable")
	}
	var raw sql.NullString
	if err := db.QueryRow(`SELECT role,session_version,folder_access FROM users WHERE username=?`, username).
		Scan(&account.role, &account.version, &raw); err != nil {
		return account, err
	}
	if raw.Valid {
		folders, err := decodeFolderAccess(raw.String)
		if err != nil {
			return account, err
		}
		account.folders = &folders
		if folders.Personal == nil {
			account.role = "viewer"
		}
	}
	return account, nil
}

func decodeFolderAccess(raw string) (folderAccess, error) {
	var folders folderAccess
	if err := json.Unmarshal([]byte(raw), &folders); err != nil {
		return folderAccess{}, err
	}
	if folders.Folders == nil || len(folders.Folders) > maxFolderGrants || !validFolderQuota(folders.QuotaBytes, folders.Personal) {
		return folderAccess{}, errors.New("invalid folder access list")
	}
	return folders, nil
}

func requestProfile(r *http.Request) (profileIdentity, bool) {
	if account, ok := r.Context().Value(profileKey{}).(profileIdentity); ok {
		return account, true
	}
	return authenticateProfile(r)
}

func authenticateProfile(r *http.Request) (profileIdentity, bool) {
	c, err := r.Cookie("fh_session")
	if err != nil {
		return profileIdentity{}, false
	}
	username, ok := parseSession(c.Value)
	if !ok {
		return profileIdentity{}, false
	}
	account, err := loadProfile(username)
	return account, err == nil && validRole(account.role)
}

func withProfile(r *http.Request, account profileIdentity) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), profileKey{}, account))
}

func requestFolders(ctx context.Context) *folderAccess {
	account, _ := ctx.Value(profileKey{}).(profileIdentity)
	return account.folders
}

func pathWithin(path, folder string) bool {
	return folder == "" || path == folder || strings.HasPrefix(path, folder+"/")
}

func profileRelative(rel string) (string, bool) {
	rel = strings.Trim(rel, "/")
	if protectedRelativePath(rel) || strings.ContainsAny(rel, "\\\x00") {
		return "", false
	}
	if rel != "" {
		for _, component := range strings.Split(rel, "/") {
			if component == "" || component == "." || component == ".." {
				return "", false
			}
		}
	}
	return rel, true
}

// canRead is also used for rows from the index. A matching prefix ends at a
// path component so granting "Family" never grants "Family private".
func (access *folderAccess) canRead(source, rel string) bool {
	if access == nil {
		return true
	}
	rel, ok := profileRelative(rel)
	if !ok {
		return false
	}
	if access.Personal != nil && access.Personal.Source == source && pathWithin(rel, access.Personal.Path) {
		return true
	}
	for _, folder := range access.Folders {
		if folder.Source == source && pathWithin(rel, folder.Path) {
			return true
		}
	}
	return false
}

func (access *folderAccess) canBrowse(source, rel string) bool {
	if access.canRead(source, rel) {
		return true
	}
	rel, ok := profileRelative(rel)
	if !ok {
		return false
	}
	if access.Personal != nil && access.Personal.Source == source && pathWithin(access.Personal.Path, rel) {
		return true
	}
	for _, folder := range access.Folders {
		if folder.Source == source && pathWithin(folder.Path, rel) {
			return true
		}
	}
	return false
}

// A configured source can itself be a symlink. Within that source a limited
// profile cannot follow symlinks, including symlinks between folder branches.
func realProfilePath(source, rel string) (string, bool) {
	rel, ok := profileRelative(rel)
	if !ok {
		return "", false
	}
	root, found := byName[source]
	if !found {
		return "", false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	full := filepath.Join(realRoot, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil || resolved != full || !publicPathInside(realRoot, full) {
		return "", false
	}
	info, err := os.Lstat(full)
	return full, err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func (access *folderAccess) visibleEntry(source, rel string) bool {
	if access == nil {
		return true
	}
	if !access.canBrowse(source, rel) {
		return false
	}
	full, ok := realProfilePath(source, rel)
	if !ok || access.canRead(source, rel) {
		return ok
	}
	info, err := os.Stat(full)
	return err == nil && info.IsDir()
}

func (access *folderAccess) readPath(source, rel string) (string, bool) {
	if access == nil {
		return safePath(source, rel)
	}
	if !access.canRead(source, rel) {
		return "", false
	}
	return realProfilePath(source, rel)
}

func (access *folderAccess) browsePath(source, rel string) (string, bool) {
	if access == nil {
		return safePath(source, rel)
	}
	if !access.canBrowse(source, rel) {
		return "", false
	}
	return realProfilePath(source, rel)
}

// sqlCondition filters before counting or limiting search results. Column
// expressions are supplied by the query, exclusively as fixed Go literals.
func (access *folderAccess) sqlCondition(sourceColumn, pathColumn string) (string, []any) {
	return access.folderSQLCondition(sourceColumn, pathColumn, false)
}

// indexedSQLCondition lets a BINARY (source,path) index find each granted
// subtree by range. '/' and '0' are consecutive bytes in SQLite BINARY order.
func (access *folderAccess) indexedSQLCondition(sourceColumn, pathColumn string) (string, []any) {
	return access.folderSQLCondition(sourceColumn, pathColumn, true)
}

func (access *folderAccess) folderSQLCondition(sourceColumn, pathColumn string, indexed bool) (string, []any) {
	if access == nil {
		return "", nil
	}
	folders := access.grants()
	if len(folders) == 0 {
		return " AND 0", nil
	}
	var terms []string
	var params []any
	for _, folder := range folders {
		if folder.Path == "" {
			terms = append(terms, sourceColumn+"=?")
			params = append(params, folder.Source)
		} else if indexed {
			// Bound the search by one primary-key range, then exclude siblings
			// between the exact folder path and the start of its descendants.
			terms = append(terms, "("+sourceColumn+"=? AND "+pathColumn+">=? AND "+pathColumn+"<? AND ("+pathColumn+"=? OR "+pathColumn+">=?))")
			params = append(params, folder.Source, folder.Path, folder.Path+"0", folder.Path, folder.Path+"/")
		} else {
			prefix := folder.Path + "/"
			terms = append(terms, "("+sourceColumn+"=? AND ("+pathColumn+"=? OR substr("+pathColumn+",1,length(?))=?))")
			params = append(params, folder.Source, folder.Path, prefix, prefix)
		}
	}
	return " AND (" + strings.Join(terms, " OR ") + ")", params
}

func requireBrowse(h http.HandlerFunc) http.HandlerFunc {
	return authenticatedHandler(h, true, false)
}

func requireBrowseMutation(h http.HandlerFunc) http.HandlerFunc {
	return requireMutation(requireBrowse(h))
}

func requireReadablePath(h http.HandlerFunc) http.HandlerFunc {
	return requireBrowse(func(w http.ResponseWriter, r *http.Request) {
		if access := requestFolders(r.Context()); access != nil {
			q := r.URL.Query()
			if _, ok := access.readPath(q.Get("source"), q.Get("path")); !ok {
				http.NotFound(w, r)
				return
			}
		}
		h(w, r)
	})
}

func handleProfileFolders(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	var role string
	var raw sql.NullString
	if err := db.QueryRowContext(r.Context(), `SELECT role,folder_access FROM users WHERE username=?`, username).Scan(&role, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "could not load folder access", http.StatusInternalServerError)
		}
		return
	}
	folders := folderAccess{Folders: []folderGrant{}}
	if raw.Valid {
		var err error
		folders, err = decodeFolderAccess(raw.String)
		if err != nil {
			http.Error(w, "could not read folder access", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"restricted": raw.Valid, "folders": folders.Folders, "personal": folders.Personal, "quotaBytes": folders.QuotaBytes, "editable": role != "admin"})
}

func setProfileFolders(w http.ResponseWriter, r *http.Request) {
	var policy struct {
		Restricted *bool         `json:"restricted"`
		Folders    []folderGrant `json:"folders"`
		Personal   *folderGrant  `json:"personal"`
		QuotaBytes int64         `json:"quotaBytes"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSmallFormBytes))
	d.DisallowUnknownFields()
	if d.Decode(&policy) != nil || d.Decode(new(any)) != io.EOF ||
		policy.Restricted == nil || len(policy.Folders) > maxFolderGrants ||
		!validFolderQuota(policy.QuotaBytes, policy.Personal) || (!*policy.Restricted && policy.QuotaBytes != 0) {
		http.Error(w, "invalid folder access policy", http.StatusBadRequest)
		return
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	var raw any
	if *policy.Restricted {
		folders, err := validateFolders(r.Context(), folderAccess{Folders: policy.Folders, Personal: policy.Personal, QuotaBytes: policy.QuotaBytes}, r.URL.Query().Get("username"), "")
		if err != nil {
			writeFolderValidationError(w, err)
			return
		}
		encoded, _ := json.Marshal(folders)
		raw = string(encoded)
	}
	result, err := db.ExecContext(r.Context(), `UPDATE users SET folder_access=? WHERE username=? AND role IN ('user','viewer')`,
		raw, r.URL.Query().Get("username"))
	if err != nil {
		http.Error(w, "could not save folder access", http.StatusInternalServerError)
		return
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		http.Error(w, "only non-administrator profiles can have folder restrictions", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errInvalidFolder = errors.New("folder unavailable")
var errPersonalFolderConflict = errors.New("personal folder already assigned or reserved")

// Called with fileMutationMu held so a profile and a pending invitation cannot
// claim overlapping personal folders concurrently.
func validateFolders(ctx context.Context, input folderAccess, username, inviteID string) (folderAccess, error) {
	folders := folderAccess{Folders: []folderGrant{}}
	if !validFolderQuota(input.QuotaBytes, input.Personal) {
		return folders, errInvalidFolder
	}
	seen := make(map[folderGrant]bool)
	for _, folder := range input.Folders {
		folder, _, err := validateFolder(folder, false)
		if err != nil {
			return folders, err
		}
		if !seen[folder] {
			folders.Folders = append(folders.Folders, folder)
			seen[folder] = true
		}
	}
	if input.Personal == nil {
		return folders, nil
	}
	personal, full, err := validateFolder(*input.Personal, true)
	if err != nil {
		return folders, err
	}
	conflict, err := personalFolderConflict(ctx, username, inviteID, full)
	if err != nil {
		return folders, err
	}
	if conflict {
		return folders, errPersonalFolderConflict
	}
	folders.Personal = &personal
	folders.QuotaBytes = input.QuotaBytes
	return folders, nil
}

func validateFolder(folder folderGrant, personal bool) (folderGrant, string, error) {
	rel, ok := profileRelative(folder.Path)
	if !ok || (personal && rel == "") {
		return folder, "", errInvalidFolder
	}
	full, ok := realProfilePath(folder.Source, rel)
	if !ok {
		return folder, "", errInvalidFolder
	}
	info, err := os.Stat(full)
	if err != nil || !info.IsDir() {
		return folder, "", errInvalidFolder
	}
	folder.Path = rel
	return folder, full, nil
}

func writeFolderValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidFolder):
		http.Error(w, "folder unavailable", http.StatusBadRequest)
	case errors.Is(err, errPersonalFolderConflict):
		http.Error(w, "personal folder already assigned or reserved", http.StatusConflict)
	default:
		http.Error(w, "could not validate folder access", http.StatusInternalServerError)
	}
}

func personalFolderConflict(ctx context.Context, username, inviteID, proposed string) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT folder_access FROM users WHERE username!=? AND folder_access IS NOT NULL
		UNION ALL SELECT i.folder_access FROM profile_invitations i WHERE i.digest!=? AND `+activeInvitation, username, inviteID, time.Now().Unix())
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return false, err
		}
		policy, err := decodeFolderAccess(raw)
		if err != nil {
			return false, err
		}
		if policy.Personal == nil {
			continue
		}
		root, ok := byName[policy.Personal.Source]
		if !ok {
			continue
		}
		existing, err := filepath.EvalSymlinks(root)
		if err != nil {
			return false, err
		}
		existing = filepath.Join(existing, filepath.FromSlash(policy.Personal.Path))
		if pathWithin(proposed, existing) || pathWithin(existing, proposed) || directoriesOverlap(proposed, existing) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Compare directory identity as well as names when the storage has multiple
// mount paths. A missing old folder still reserves its configured pathname.
func directoriesOverlap(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr != nil || bErr != nil {
		return false
	}
	for left, right := a, b; ; {
		leftInfo, _ := os.Stat(left)
		rightInfo, _ := os.Stat(right)
		if (leftInfo != nil && os.SameFile(leftInfo, bInfo)) || (rightInfo != nil && os.SameFile(rightInfo, aInfo)) {
			return true
		}
		nextLeft, nextRight := filepath.Dir(left), filepath.Dir(right)
		if nextLeft == left && nextRight == right {
			return false
		}
		left, right = nextLeft, nextRight
	}
}

func requireUploadMutation(h http.HandlerFunc) http.HandlerFunc {
	return requireMutation(authenticatedHandler(h, true, true))
}

func profileWriteAllowed(w http.ResponseWriter, r *http.Request, source, path string, includeRoot bool) bool {
	account, ok := r.Context().Value(profileKey{}).(profileIdentity)
	if !ok {
		return true
	} // direct calls in unit tests; HTTP routes always authenticate
	current, err := loadProfile(account.username)
	if err == nil && current.version == account.version && current.role != "viewer" {
		if current.folders == nil && account.folders == nil {
			return true
		}
		if current.folders != nil && current.folders.canWrite(source, path, includeRoot) {
			if _, exists := realProfilePath(source, path); exists {
				return true
			}
		}
	}
	http.Error(w, "write access is limited to the personal folder", http.StatusForbidden)
	return false
}

func handleProfileStorage(w http.ResponseWriter, r *http.Request) {
	access := requestFolders(r.Context())
	var personal *folderGrant
	if access != nil {
		personal = access.Personal
	}
	account, _ := requestProfile(r)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"restricted": access != nil, "personal": personal, "canWrite": account.role != "viewer"})
}
