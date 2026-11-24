package main

import "database/sql"

// Existing profiles receive an ID once. A profile recreated with the same
// login receives a different ID independently of its session version.
func initShareOwners(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('users') WHERE name='account_id'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.Exec(`ALTER TABLE users ADD COLUMN account_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`UPDATE users SET account_id=lower(hex(randomblob(16))) WHERE account_id='';
		CREATE UNIQUE INDEX IF NOT EXISTS filehub_account_id ON users(account_id) WHERE account_id!='';
		CREATE TRIGGER IF NOT EXISTS filehub_assign_account_id AFTER INSERT ON users
		WHEN NEW.account_id='' BEGIN
			UPDATE users SET account_id=lower(hex(randomblob(16))) WHERE username=NEW.username;
		END;`)
	return err
}
