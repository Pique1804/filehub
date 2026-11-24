// Admin-only user management endpoints.
package main

import (
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func validUsername(u string) bool {
	if len(u) < 1 || len(u) > 32 {
		return false
	}
	for _, c := range u {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validRole(r string) bool { return r == "admin" || r == "user" || r == "viewer" }

func handleUsersList(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT username,role,created,folder_access IS NOT NULL FROM users ORDER BY role,username`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var u, role string
		var created int64
		var restricted bool
		if rows.Scan(&u, &role, &created, &restricted) == nil {
			out = append(out, map[string]any{"username": u, "role": role, "created": created, "restricted": restricted})
		}
	}
	writeJSON(w, map[string]any{"users": out})
}

func handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	username, password, role := r.PostFormValue("username"), r.PostFormValue("password"), r.PostFormValue("role")
	if !validUsername(username) {
		http.Error(w, "плохой логин (a-z, 0-9, . _ -)", http.StatusBadRequest)
		return
	}
	if len(password) < minPasswordLength {
		http.Error(w, "пароль минимум 12 символов", http.StatusBadRequest)
		return
	}
	if !validRole(role) {
		role = "user"
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "не удалось задать пароль", http.StatusBadRequest)
		return
	}
	version, err := newSessionVersion()
	if err != nil {
		http.Error(w, "не удалось создать пользователя", http.StatusInternalServerError)
		return
	}
	var access any
	if r.PostFormValue("restricted") == "1" && role != "admin" {
		access = `{"folders":[]}`
	}
	if _, err := db.Exec(`INSERT INTO users(username,pwhash,role,created,session_version,folder_access) VALUES(?,?,?,?,?,?)`,
		username, string(h), role, time.Now().Unix(), version, access); err != nil {
		http.Error(w, "пользователь уже существует", http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleUserDelete(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	// Keep the administrator check and removal in the same SQLite statement.
	result, err := db.ExecContext(r.Context(), `DELETE FROM users WHERE username=? AND (
		COALESCE(role,'')!='admin' OR EXISTS (SELECT 1 FROM users WHERE role='admin' AND username!=?)
	)`, username, username)
	if err != nil {
		http.Error(w, "не удалось удалить пользователя", http.StatusInternalServerError)
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		http.Error(w, "не удалось проверить удаление пользователя", http.StatusInternalServerError)
		return
	}
	if affected == 0 && userRole(username) != "" {
		http.Error(w, "нельзя удалить последнего админа", http.StatusBadRequest)
		return
	}
	davInvalidate(username)
	writeJSON(w, map[string]any{"ok": true})
}

func handleUserPassword(w http.ResponseWriter, r *http.Request) {
	if !parseSmallForm(w, r) {
		return
	}
	username, password := r.PostFormValue("username"), r.PostFormValue("password")
	if userRole(username) == "" {
		http.Error(w, "нет такого пользователя", http.StatusNotFound)
		return
	}
	if len(password) < minPasswordLength {
		http.Error(w, "пароль минимум 12 символов", http.StatusBadRequest)
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "не удалось изменить пароль", http.StatusBadRequest)
		return
	}
	version, err := newSessionVersion()
	if err != nil {
		http.Error(w, "не удалось изменить пароль", http.StatusInternalServerError)
		return
	}
	result, err := db.Exec(`UPDATE users SET pwhash=?,session_version=? WHERE username=?`, string(h), version, username)
	if err != nil {
		http.Error(w, "не удалось изменить пароль", http.StatusInternalServerError)
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		http.Error(w, "нет такого пользователя", http.StatusNotFound)
		return
	}
	davInvalidate(username) // old password must stop working over WebDAV now
	writeJSON(w, map[string]any{"ok": true})
}

func handleUserRole(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	username, role := q.Get("username"), q.Get("role")
	if !validRole(role) {
		http.Error(w, "плохая роль", http.StatusBadRequest)
		return
	}
	fileMutationMu.Lock()
	defer fileMutationMu.Unlock()
	result, err := db.ExecContext(r.Context(), `UPDATE users SET role=? WHERE username=? AND (
		?='admin' OR COALESCE(role,'')!='admin' OR EXISTS (SELECT 1 FROM users WHERE role='admin' AND username!=?)
	) AND (?!='admin' OR folder_access IS NULL)`, role, username, role, username, role)
	if err != nil {
		http.Error(w, "не удалось изменить роль", http.StatusInternalServerError)
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		http.Error(w, "не удалось проверить изменение роли", http.StatusInternalServerError)
		return
	}
	if affected == 0 && userRole(username) != "" {
		if role == "admin" {
			http.Error(w, "сначала отключите ограничения папок", http.StatusConflict)
			return
		}
		http.Error(w, "нельзя понизить последнего админа", http.StatusBadRequest)
		return
	}
	davInvalidate(username)
	writeJSON(w, map[string]any{"ok": true})
}
