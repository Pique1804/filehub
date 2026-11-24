package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const activeInvitation = `i.expires>? AND EXISTS (
	SELECT 1 FROM users u WHERE u.username=i.issuer AND u.role='admin' AND u.session_version=i.issuer_version
)`

func initInvitations() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS profile_invitations(
		digest TEXT PRIMARY KEY NOT NULL,
		issuer TEXT NOT NULL,
		issuer_version TEXT NOT NULL,
		role TEXT NOT NULL CHECK(role IN ('user','viewer')),
		label TEXT NOT NULL,
		folder_access TEXT NOT NULL,
		expires INTEGER NOT NULL
	)`)
	return err
}

func registerInvitations(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/invitations", requireAdmin(listInvitations))
	mux.HandleFunc("POST /api/admin/invitations", requireAdminMutation(createInvitation))
	mux.HandleFunc("POST /api/admin/invitations/revoke", requireAdminMutation(revokeInvitation))
	mux.HandleFunc("POST /api/invite/accept", requireMutation(acceptInvitation))
}

func inviteDigest(token string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return "", false
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), true
}

func createInvitation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var request struct {
		folderAccess
		Role  string `json:"role"`
		Label string `json:"label"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSmallFormBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF ||
		(request.Role != "user" && request.Role != "viewer") || utf8.RuneCountInString(request.Label) > 80 ||
		len(request.Folders) > maxFolderGrants || (request.Personal == nil && len(request.Folders) == 0) {
		http.Error(w, "invalid_invitation", http.StatusBadRequest)
		return
	}
	admin, ok := requestProfile(r)
	if !ok || admin.role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "invitation_unavailable", http.StatusInternalServerError)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest, _ := inviteDigest(token)
	ctx := r.Context()

	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	policy, err := validateFolders(ctx, request.folderAccess, "", "")
	if err != nil {
		writeFolderValidationError(w, err)
		return
	}
	encoded, _ := json.Marshal(policy)
	now := time.Now()
	if _, err := db.ExecContext(ctx, `DELETE FROM profile_invitations AS i WHERE NOT (`+activeInvitation+`)`, now.Unix()); err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	expires := now.Add(24 * time.Hour).Unix()
	result, err := db.ExecContext(ctx, `INSERT INTO profile_invitations(digest,issuer,issuer_version,role,label,folder_access,expires)
		SELECT ?,u.username,u.session_version,?,?,?,? FROM users u WHERE u.username=? AND u.role='admin' AND u.session_version=?`,
		digest, request.Role, strings.TrimSpace(request.Label), string(encoded), expires, admin.username, admin.version)
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{"token": token, "expires": expires})
}

func listInvitations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	type invitation struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Role    string `json:"role"`
		Expires int64  `json:"expires"`
	}
	rows, err := db.QueryContext(r.Context(), `SELECT i.digest,i.label,i.role,i.expires FROM profile_invitations i
		WHERE `+activeInvitation+` ORDER BY i.expires DESC`, time.Now().Unix())
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	out := []invitation{}
	for rows.Next() {
		var item invitation
		if rows.Scan(&item.ID, &item.Label, &item.Role, &item.Expires) != nil {
			http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
			return
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, map[string]any{"invitations": out})
}

func revokeInvitation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := r.URL.Query().Get("id")
	if raw, err := hex.DecodeString(id); err != nil || len(raw) != sha256.Size {
		http.Error(w, "invalid_invitation", http.StatusBadRequest)
		return
	}
	if _, err := db.ExecContext(r.Context(), `DELETE FROM profile_invitations WHERE digest=?`, id); err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func acceptInvitation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !parseSmallForm(w, r) {
		return
	}
	key := "invitation:" + loginKey(r)
	if !loginAllowed(key) {
		w.Header().Set("Retry-After", loginRetryAfter(key))
		http.Error(w, "too_many_attempts", http.StatusTooManyRequests)
		return
	}
	digest, ok := inviteDigest(r.PostFormValue("token"))
	if !ok {
		recordLoginFailure(key)
		http.Error(w, "invitation_inactive", http.StatusGone)
		return
	}
	username, password := strings.TrimSpace(r.PostFormValue("username")), r.PostFormValue("password")
	if !validUsername(username) || len(password) < minPasswordLength || len(password) > 72 {
		http.Error(w, "invalid_credentials", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	var role, encoded string
	err := db.QueryRowContext(ctx, `SELECT i.role,i.folder_access FROM profile_invitations i WHERE i.digest=? AND `+
		activeInvitation, digest, time.Now().Unix()).Scan(&role, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		recordLoginFailure(key)
		http.Error(w, "invitation_inactive", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "invalid_credentials", http.StatusBadRequest)
		return
	}
	version, err := newSessionVersion()
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	policy, err := decodeFolderAccess(encoded)
	if err != nil || (role != "viewer" && role != "user") {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}

	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	// Another recipient may have accepted this link while bcrypt was running.
	// Check that the invitation still exists before examining its reservation.
	var active int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM profile_invitations i WHERE i.digest=? AND `+
		activeInvitation, digest, time.Now().Unix()).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "invitation_inactive", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err := validateFolders(ctx, policy, "", digest); err != nil {
		http.Error(w, "invitation_folders_unavailable", http.StatusConflict)
		return
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback()
	// Claim and account creation commit together. A taken login rolls the
	// claim back so the recipient can retry with a different username.
	now := time.Now().Unix()
	claim, err := tx.ExecContext(ctx, `DELETE FROM profile_invitations AS i WHERE i.digest=? AND `+activeInvitation, digest, now)
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	if claimed, err := claim.RowsAffected(); err != nil || claimed != 1 {
		http.Error(w, "invitation_inactive", http.StatusGone)
		return
	}
	insert, err := tx.ExecContext(ctx, `INSERT INTO users(username,pwhash,role,created,session_version,folder_access)
		VALUES (?,?,?,?,?,?) ON CONFLICT(username) DO NOTHING`, username, string(hash), role, now, version, encoded)
	if err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	if inserted, err := insert.RowsAffected(); err != nil || inserted != 1 {
		http.Error(w, "username_taken", http.StatusConflict)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "invitation_unavailable", http.StatusServiceUnavailable)
		return
	}
	clearLoginFailures(key)
	setSession(w, r, username, version)
	w.WriteHeader(http.StatusNoContent)
}
