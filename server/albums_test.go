package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlbumLinkCanBeRevokedAndReissued(t *testing.T) {
	maintenanceFixture(t)
	if _, err := db.Exec(`INSERT INTO albums VALUES(1,'Trip',1);
		INSERT INTO users(username,role) VALUES('publisher','user')`); err != nil {
		t.Fatal(err)
	}
	profile, err := loadProfile("publisher")
	if err != nil {
		t.Fatal(err)
	}
	change := func(handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		result := httptest.NewRecorder()
		handler(result, withProfile(httptest.NewRequest(http.MethodPost, "/?id=1", nil), profile))
		if result.Code != http.StatusOK {
			t.Fatalf("album operation returned %d: %s", result.Code, result.Body.String())
		}
		return result
	}
	share := func() string {
		t.Helper()
		var link struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(change(handleAlbumShare).Body.Bytes(), &link); err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(link.URL, "/a/")
	}
	token := share()
	if token == "" || albumForToken(token) != 1 || share() != token {
		t.Fatal("an active link should be valid and reusable")
	}
	change(handleAlbumRevokeLink)
	if albumForToken(token) != 0 {
		t.Fatal("the holder could still use the revoked link")
	}
	next := share()
	if next == token || albumForToken(next) != 1 {
		t.Fatal("sharing again should issue a fresh link")
	}
	if _, err := db.Exec(`UPDATE users SET role='viewer' WHERE username='publisher';
		UPDATE users SET role='user' WHERE username='publisher'`); err != nil {
		t.Fatal(err)
	}
	if albumForToken(next) != 0 {
		t.Fatal("an old link should stay revoked after access is restored")
	}
}
