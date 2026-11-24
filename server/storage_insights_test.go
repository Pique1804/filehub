package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeStorageFixture(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0600); err != nil {
		t.Fatal(err)
	}
	return full
}

func allocatedStorageFixture(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, _ := storageFileStat(info)
	return stat.Blocks * 512
}

func TestStorageVolumeUsageCountsBlocksAndHardLinksOnce(t *testing.T) {
	temp := t.TempDir()
	first, second := filepath.Join(temp, "first"), filepath.Join(temp, "second")
	oldSources, oldPreview, oldThumb := sources, previewDir, thumbDir
	t.Cleanup(func() { sources, previewDir, thumbDir = oldSources, oldPreview, oldThumb })
	sources = []Source{{Name: "First", Root: first}, {Name: "Alias", Root: first}, {Name: "Second", Root: second}}
	previewDir = filepath.Join(temp, "preview")
	thumbDir = previewDir // configuring the same cache twice does not double it
	content := bytes.Repeat([]byte("fixture"), 2000)
	trash := writeStorageFixture(t, first, ".filehub-trash/item", content)
	version := writeStorageFixture(t, second, ".filehub-versions/original", content)
	preview := writeStorageFixture(t, previewDir, "cached.pdf", content)
	if err := os.Link(trash, filepath.Join(first, ".filehub-trash/second-name")); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(temp, "tus")
	const incompleteID = "abcdabcdabcdabcd"
	partial := writeStorageFixture(t, staging, incompleteID, []byte("partial"))
	meta := writeStorageFixture(t, staging, incompleteID+".info", []byte(`{"ID":"abcdabcdabcdabcd","Size":50}`))
	const finished = "bbbbbbbbbbbbbbbb"
	writeStorageFixture(t, staging, finished, []byte("completed"))
	writeStorageFixture(t, staging, finished+".info", []byte(`{"ID":"bbbbbbbbbbbbbbbb","Size":9}`))

	volumes, incomplete := storageVolumeBreakdown(context.Background(), staging)
	if incomplete || len(volumes) != 1 || len(volumes[0].Sources) != 3 {
		t.Fatalf("wrong volumes %+v, incomplete %v", volumes, incomplete)
	}
	got := volumes[0]
	if got.Total == 0 || got.Trash != allocatedStorageFixture(t, trash) ||
		got.Versions != allocatedStorageFixture(t, version) || got.Previews != allocatedStorageFixture(t, preview) ||
		got.Uploads != allocatedStorageFixture(t, partial)+allocatedStorageFixture(t, meta) {
		t.Fatalf("wrong on-disk totals: %+v", got)
	}
}

func TestStorageFoldersAndDuplicateSavings(t *testing.T) {
	temp := t.TempDir()
	oldDB, oldIndexed, oldByName := db, indexedN, byName
	t.Cleanup(func() {
		if db != nil {
			db.Close()
		}
		db, indexedN, byName = oldDB, oldIndexed, oldByName
	})
	if err := initSearch(filepath.Join(temp, "index.db")); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(temp, "first"), filepath.Join(temp, "second")
	byName = map[string]string{"First": first, "Second": second}
	payload := bytes.Repeat([]byte("A"), 12000)
	origin := writeStorageFixture(t, first, "media/a.txt", payload)
	other := writeStorageFixture(t, first, "media/deep/b.txt", payload)
	third := writeStorageFixture(t, second, "c.txt", payload)
	writeStorageFixture(t, first, "notes/different.txt", bytes.Repeat([]byte("B"), len(payload)))
	if err := os.Link(origin, filepath.Join(second, "shared.txt")); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []duplicatePath{
		{"First", "media/a.txt"}, {"First", "media/deep/b.txt"}, {"Second", "c.txt"},
		{"First", "notes/different.txt"}, {"Second", "shared.txt"},
	} {
		if _, err := db.Exec(`INSERT INTO files VALUES(?,?,?,0,?,0)`, filepath.Base(entry.Path), entry.Path, entry.Source, len(payload)); err != nil {
			t.Fatal(err)
		}
	}
	folders, partial, err := indexedStorageFolders(context.Background(), "First", "")
	if err != nil || partial || len(folders) != 2 || folders[0].Path != "media" || folders[0].Files != 2 || folders[0].Bytes != 2*int64(len(payload)) {
		t.Fatalf("indexed folders: %+v, partial %v, error %v", folders, partial, err)
	}
	nested, partial, err := indexedStorageFolders(context.Background(), "First", "media")
	if err != nil || partial || len(nested) != 1 || nested[0].Path != "media/deep" || nested[0].Bytes != int64(len(payload)) {
		t.Fatalf("nested folders: %+v, partial %v, error %v", nested, partial, err)
	}
	all, err := scanIndexedDuplicates(context.Background(), "", "")
	if err != nil || all.Partial || len(all.Groups) != 1 || all.Checked != 4 {
		t.Fatalf("duplicate groups: %+v, error %v", all, err)
	}
	group := all.Groups[0]
	savings := allocatedStorageFixture(t, other) + allocatedStorageFixture(t, third)
	if group.Copies != 3 || group.PathCount != 4 || group.Size != int64(len(payload)) ||
		group.Savings != savings || all.Savings != savings {
		t.Fatalf("hard link counted incorrectly: %+v", all)
	}
	scoped, err := scanIndexedDuplicates(context.Background(), "First", "notes")
	if err != nil || scoped.Partial || len(scoped.Groups) != 0 {
		t.Fatalf("duplicate escaped folder scope: %+v, error %v", scoped, err)
	}
	if contents, err := os.ReadFile(origin); err != nil || !bytes.Equal(contents, payload) {
		t.Fatalf("scan changed an original: %v", err)
	}
}
