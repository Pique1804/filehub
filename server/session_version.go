package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Existing accounts keep the empty version so deployed legacy cookies survive
// this additive migration. Their first password change revokes all those cookies.
func initSessionVersions() error {
	rows, err := db.Query(`PRAGMA table_info(users)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var id, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&id, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		found = found || name == "session_version"
	}
	err = rows.Err()
	rows.Close()
	if err != nil || found {
		return err
	}
	_, err = db.Exec(`ALTER TABLE users ADD COLUMN session_version TEXT NOT NULL DEFAULT ''`)
	return err
}

func newSessionVersion() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func userSessionVersion(username string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("auth database unavailable")
	}
	var version string
	err := db.QueryRow(`SELECT session_version FROM users WHERE username=?`, username).Scan(&version)
	return version, err
}

func initRevokedSessions() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS revoked_sessions(token_hash TEXT PRIMARY KEY, expires INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS revoked_sessions_expiry ON revoked_sessions(expires);`)
	return err
}
