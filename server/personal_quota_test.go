package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func quotaFixture(t *testing.T) *receiptFixture {
	t.Helper()
	previous := quotaTUSDirectory
	t.Cleanup(func() { quotaTUSDirectory = previous })
	f := newReceiptFixture(t)
	if err := os.Mkdir(filepath.Join(f.root, "personal"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET folder_access='{"folders":[],"personal":{"source":"T","path":"personal"},"quotaBytes":10}' WHERE username=?`, "writer"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "personal", ".existing"), []byte("123"), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

func quotaFixtureCreate(f *receiptFixture, size int) *httptest.ResponseRecorder {
	field := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	return f.call("POST", "/files/", "writer", nil, map[string]string{
		"Upload-Length": strconv.Itoa(size), "Upload-Metadata": "filename " + field("quota.txt") + ",source " + field("T") + ",path " + field("personal"),
	})
}

func TestPersonalQuotaReservesConcurrentAndResumedUploads(t *testing.T) {
	f := quotaFixture(t)
	responses := make(chan *httptest.ResponseRecorder, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() { defer group.Done(); responses <- quotaFixtureCreate(f, 6) }()
	}
	group.Wait()
	close(responses)
	var location string
	var admitted, refused int
	for response := range responses {
		switch response.Code {
		case http.StatusCreated:
			admitted++
			location = response.Header().Get("Location")
		case http.StatusInsufficientStorage:
			refused++
		default:
			t.Fatalf("unexpected admission %d: %s", response.Code, response.Body)
		}
	}
	if admitted != 1 || refused != 1 {
		t.Fatalf("admitted=%d, refused=%d", admitted, refused)
	}
	if r := f.patch(location, "0", []byte("123")); r.Code != http.StatusNoContent {
		t.Fatal(r.Code, r.Body)
	}
	f.restart(t)
	var response struct {
		Quota *personalQuotaUsage `json:"quota"`
	}
	status := f.call("GET", "/api/profile/quota", "writer", nil, nil)
	if err := json.Unmarshal(status.Body.Bytes(), &response); err != nil || response.Quota == nil || response.Quota.Used != 3 || response.Quota.Reserved != 6 || response.Quota.Available != 1 {
		t.Fatalf("usage after restart %d: %s (%v)", status.Code, status.Body, err)
	}
	if r := f.call("DELETE", location, "other", nil, nil); r.Code != http.StatusNotFound && r.Code != http.StatusForbidden {
		t.Fatalf("other account canceled upload: %d", r.Code)
	}
	if r := f.call("DELETE", location, "writer", nil, nil); r.Code != http.StatusNoContent {
		t.Fatal(r.Code, r.Body)
	}
	location = f.createAt(t, "7", "personal")
	if r := f.patch(location, "0", []byte("content")); r.Code != http.StatusNoContent {
		t.Fatal(r.Code, r.Body)
	}
	if r := f.call("GET", receiptURL(location), "writer", nil, nil); r.Code != http.StatusOK {
		t.Fatal(r.Code, r.Body)
	}
	if r := quotaFixtureCreate(f, 1); r.Code != http.StatusInsufficientStorage {
		t.Fatal(r.Code, r.Body)
	}
}

func TestQuotaRejectsPublicationAfterExternalFilesUseTheSpace(t *testing.T) {
	f := quotaFixture(t)
	loc := f.createAt(t, "7", "personal")
	if err := os.WriteFile(filepath.Join(f.root, "personal", "external"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := f.patch(loc, "0", []byte("content")); r.Code != http.StatusInsufficientStorage {
		t.Fatal(r.Code, r.Body)
	}
	if _, err := os.Stat(filepath.Join(f.root, "personal", "phone.mp4")); !os.IsNotExist(err) {
		t.Fatalf("file was published over quota: %v", err)
	}
	if r := f.call("HEAD", loc, "writer", nil, nil); r.Code != http.StatusInsufficientStorage {
		t.Fatal(r.Code, r.Body)
	}
	if r := f.call("DELETE", loc, "writer", nil, nil); r.Code != http.StatusNoContent {
		t.Fatal(r.Code, r.Body)
	}
	if r := quotaFixtureCreate(f, 6); r.Code != http.StatusCreated {
		t.Fatal(r.Code, r.Body)
	}
}

func TestPersonalQuotaCountsActiveStreamsAlongsideTUS(t *testing.T) {
	f := quotaFixture(t)
	account, err := loadProfile("writer")
	if err != nil {
		t.Fatal(err)
	}
	fileMutationMu.Lock()
	reservation, ceiling, err := reservePersonalWrite(context.Background(), account, 6, 0, 100)
	fileMutationMu.Unlock()
	if err != nil || reservation == nil || ceiling == nil || *ceiling != 7 {
		t.Fatalf("stream reservation: %v", err)
	}
	t.Cleanup(reservation.release)
	if r := quotaFixtureCreate(f, 2); r.Code != http.StatusInsufficientStorage {
		t.Fatal(r.Code, r.Body)
	}
	reservation.release()
	if r := quotaFixtureCreate(f, 7); r.Code != http.StatusCreated {
		t.Fatal(r.Code, r.Body)
	}
}

func TestDirectUploadAtQuotaRecognizesAnIdenticalRetry(t *testing.T) {
	f := quotaFixture(t)
	f.mux.HandleFunc("/api/upload", requireUploadMutation(handleUpload))
	for name, contents := range map[string]string{"memo.txt": "123", "memo (1).txt": "memo"} {
		if err := os.WriteFile(filepath.Join(f.root, "personal", name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, unknown := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/api/upload?source=T&path=/personal/", strings.NewReader("memo"))
		if unknown {
			req.ContentLength = -1
		}
		req.AddCookie(&http.Cookie{Name: "fh_session", Value: signSession("writer")})
		req.Header.Set("X-Filename", "memo.txt")
		response := httptest.NewRecorder()
		f.mux.ServeHTTP(response, req)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"memo (1).txt"`) ||
			!strings.Contains(response.Body.String(), `"identical":true`) {
			t.Fatalf("unknown length %v: %d %s", unknown, response.Code, response.Body)
		}
	}
	staged, err := filepath.Glob(filepath.Join(f.root, "personal", ".filehub-upload-*"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("unexpected staging files %v: %v", staged, err)
	}
}
