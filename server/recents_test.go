package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"
)

func recentItems(t *testing.T, folders *folderAccess) []recentFile {
	t.Helper()
	w := httptest.NewRecorder()
	r := withProfile(httptest.NewRequest(http.MethodGet, "/api/recents", nil), profileIdentity{folders: folders})
	handleRecents(w, r)
	var response struct{ Items []recentFile }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK {
		t.Fatalf("recents: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	return response.Items
}

func TestRecentIndexProfileAndMutations(t *testing.T) {
	maintenanceFixture(t)
	defer recentIndexDB.Store(nil)
	const folder = "Фото_%"
	const original = folder + "/note.txt"
	const nearby = folder + "-private/note.txt"
	const newFile = folder + "/new.txt"
	const renamed = "другая папка"
	const moved = renamed + "/new.txt"
	add := func(source, rel string, mtime int64) {
		t.Helper()
		if err := indexAdd(source, rel, path.Base(rel), false, 17, mtime); err != nil {
			t.Fatal(err)
		}
	}
	add("T", original, 1)
	add("Other", original, 2)
	add("T", nearby, 3)
	add("T", folder+"/cache/ignored.txt", 4)
	grant := &folderAccess{Folders: []folderGrant{{Source: "T", Path: folder}}}
	wantOriginal := recentFile{Name: "note.txt", Path: original, Source: "T", ModTime: 1, Size: 17}
	if err := initRecentIndex(); err != nil {
		t.Fatal(err)
	}
	if got := recentItems(t, grant); !reflect.DeepEqual(got, []recentFile{wantOriginal}) {
		t.Fatalf("legacy scoped recents = %+v", got)
	}
	if got := recentItems(t, &folderAccess{}); len(got) != 0 {
		t.Fatalf("empty grants exposed %+v", got)
	}
	add("T", newFile, 5)
	add("T", newFile, 6)
	if got := recentItems(t, grant); len(got) != 2 || got[0].Path != newFile || got[0].ModTime != 6 {
		t.Fatalf("recents after update = %+v", got)
	}
	if err := remapFileReferences("T", folder, "Other", renamed); err != nil {
		t.Fatal(err)
	}
	if got := recentItems(t, grant); len(got) != 0 {
		t.Fatalf("old grant exposed moved entries %+v", got)
	}
	if got := recentItems(t, nil); len(got) != 4 || got[0].Path != moved || got[0].Source != "Other" {
		t.Fatalf("recents after move = %+v", got)
	}
	if err := updateFileReferences("Other", renamed, "Other", ".filehub-trash/123", true); err != nil {
		t.Fatal(err)
	}
	if got := recentItems(t, nil); len(got) != 2 || got[0].Path != nearby || got[1].Path != original || got[1].Source != "Other" {
		t.Fatalf("recents after trash = %+v", got)
	}
	add("T", original, 7)
	if got := recentItems(t, grant); len(got) != 1 || got[0].Path != original || got[0].ModTime != 7 {
		t.Fatalf("recents after re-add = %+v", got)
	}
	if err := indexDelete("T", original); err != nil {
		t.Fatal(err)
	}
	if got := recentItems(t, grant); len(got) != 0 {
		t.Fatalf("deleted item still present: %+v", got)
	}
}

func TestRecentIndexRefreshAfterOlderVersionAndReindex(t *testing.T) {
	root := maintenanceFixture(t)
	defer recentIndexDB.Store(nil)
	if err := initRecentIndex(); err != nil {
		t.Fatal(err)
	}
	if err := indexAdd("T", "stale.txt", "stale.txt", false, 1, 1); err != nil {
		t.Fatal(err)
	}
	// An older binary updates only the FTS table.
	if _, err := db.Exec(`DELETE FROM files; INSERT INTO files VALUES('added.txt','added.txt','T',0,3,2)`); err != nil {
		t.Fatal(err)
	}
	recentIndexDB.Store(nil)
	if err := initRecentIndex(); err != nil {
		t.Fatal(err)
	}
	if got := recentItems(t, nil); len(got) != 1 || got[0].Path != "added.txt" {
		t.Fatalf("recents after restart = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "on-disk.txt"), []byte("sample"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := rebuildFileIndex(); err != nil {
		t.Fatal(err)
	}
	if got := recentItems(t, nil); len(got) != 1 || got[0].Path != "on-disk.txt" {
		t.Fatalf("recents after reindex = %+v", got)
	}
}
