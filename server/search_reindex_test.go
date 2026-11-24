package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReindexPrunesMissingTextAndAllowsMoveRetry(t *testing.T) {
	previousPreview, previousIndexed := previewDir, indexedN
	previewDir = t.TempDir()
	t.Cleanup(func() { previewDir, indexedN = previousPreview, previousIndexed })
	origin := maintenanceFixture(t)
	target := t.TempDir()
	missing := filepath.Join(t.TempDir(), "active")
	if err := os.Mkdir(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, source := range []Source{{Name: "target", Root: target}, {Name: "offline", Root: missing}} {
		sources = append(sources, source)
		byName[source.Name] = source.Root
	}

	exec := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	count := func(table, source, path string, want int) {
		t.Helper()
		var found int
		if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE source=? AND path=?`, source, path).Scan(&found); err != nil || found != want {
			t.Fatalf("%s %s:%s count=%d, want %d (error %v)", table, source, path, found, want, err)
		}
	}
	seedText := func(source, path, body string) {
		t.Helper()
		id := exec(`INSERT INTO content(body,source,path,name,mtime) VALUES(?,?,?,?,1)`, body, source, path, filepath.Base(path))
		exec(`INSERT INTO content_meta(source,path,size,mtime,method,chars,rowid_fts) VALUES(?,?,?,1,'plain',?,?)`,
			source, path, len(body), len(body), id)
	}
	writeOriginal := func(root, path, text string) {
		t.Helper()
		file := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	const filename = "move.txt"
	const kept = ".hidden/exists.txt"
	for _, entry := range []struct{ root, source, path, text string }{
		{origin, "T", filename, "source review"},
		{target, "target", filename, "old destination"},
		{target, "target", kept, "excluded from filename search"},
		{missing, "offline", filename, "offline original"},
	} {
		writeOriginal(entry.root, entry.path, entry.text)
		seedText(entry.source, entry.path, entry.text)
	}

	const owned = "previously-referenced.txt"
	seedText("target", owned, "missing file with retained user records")
	exec(`INSERT INTO albums VALUES(1,'fixture',1)`)
	exec(`INSERT INTO album_photos VALUES(1,'target',?,1)`, owned)
	exec(`INSERT INTO shares(token,source,path,isdir,pwhash,expires,created) VALUES('keep','target',?,0,'',0,1)`, owned)
	exec(`INSERT INTO favorites VALUES('target',?,1)`, owned)
	exec(`INSERT INTO photo_versions VALUES('version','target',?,'target','.filehub-versions/keep',1,'rotate',1,'digest')`, owned)
	exec(`INSERT INTO path_aliases VALUES('target',?,'target','backup-original.txt')`, owned)
	exec(`INSERT INTO path_alias_history VALUES('target',?,'target','backup-original.txt')`, owned)

	if err := os.Rename(filepath.Join(target, filename), filepath.Join(target, "renamed-externally.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(missing, missing+".unavailable"); err != nil {
		t.Fatal(err)
	}
	if _, err := rebuildFileIndex(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"content", "content_meta"} {
		count(table, "target", filename, 0)
		count(table, "target", owned, 0)
		count(table, "target", kept, 1)
		count(table, "T", filename, 1)
		count(table, "offline", filename, 1)
	}
	for _, table := range []string{"album_photos", "shares", "favorites", "photo_versions", "path_aliases", "path_alias_history"} {
		count(table, "target", owned, 1)
	}

	stat, err := os.Stat(filepath.Join(origin, filename))
	if err != nil {
		t.Fatal(err)
	}
	receipt := uploadReceipt{Source: "T", Path: filename, Size: stat.Size(), Modified: stat.ModTime().UnixNano()}
	response := httptest.NewRecorder()
	handleMove(response, httptest.NewRequest(http.MethodPost, "/api/move?source=T&path=move.txt&destSource=target", nil))
	if response.Code != http.StatusOK || !receiptCurrent(receipt) {
		t.Fatalf("retry status=%d, body=%s, receipt current=%t", response.Code, response.Body, receiptCurrent(receipt))
	}
	for path, want := range map[string]string{
		filepath.Join(target, filename):                 "source review",
		filepath.Join(target, "renamed-externally.txt"): "old destination",
		filepath.Join(missing+".unavailable", filename): "offline original",
	} {
		if body, err := os.ReadFile(path); err != nil || string(body) != want {
			t.Fatalf("%s contents=%q, want %q (error %v)", path, body, want, err)
		}
	}
}
