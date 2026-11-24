package main

import (
	"database/sql"
	"sync/atomic"
)

var recentIndexDB atomic.Pointer[sql.DB]

// Refresh the derived index from SQLite on startup. An older binary may have
// changed the search index after a rollback without maintaining this table.
func initRecentIndex() error {
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS file_recents (
		source TEXT NOT NULL, path TEXT NOT NULL, name TEXT NOT NULL,
		size INTEGER NOT NULL, mtime INTEGER NOT NULL,
		PRIMARY KEY(source,path)
	) WITHOUT ROWID`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS file_recents_by_mtime ON file_recents(mtime DESC)`); err != nil {
		return err
	}
	if err := refreshRecentIndex(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	recentIndexDB.Store(db)
	return nil
}

func recentIndexReady() bool {
	return db != nil && recentIndexDB.Load() == db
}

func refreshRecentIndex(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM file_recents`); err != nil {
		return err
	}
	// Order by rowid so the last indexed entry wins if a legacy database
	// contains duplicate source/path pairs.
	_, err := tx.Exec(`INSERT OR REPLACE INTO file_recents(source,path,name,size,mtime)
		SELECT source,path,name,size,mtime FROM files WHERE isdir=0 ORDER BY rowid`)
	return err
}
