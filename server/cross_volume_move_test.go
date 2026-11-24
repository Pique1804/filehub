package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCrossVolumeMoveKeepsUploadReceiptCurrent(t *testing.T) {
	origin := maintenanceFixture(t)
	destination, err := os.MkdirTemp("/dev/shm", "filehub-move-")
	if err != nil {
		t.Skipf("independent test volume unavailable: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(destination) })
	originInfo, err := backupDirectoryInfo(origin)
	if err != nil {
		t.Fatal(err)
	}
	destInfo, err := backupDirectoryInfo(destination)
	if err != nil {
		t.Fatal(err)
	}
	if originInfo.Device == destInfo.Device {
		t.Skip("temporary source and destination use one volume")
	}
	byName["TARGET"] = destination

	for _, movePath := range []string{"file.jpg", "folder"} {
		t.Run(movePath, func(t *testing.T) {
			const original = "fixture-upload"
			rel := movePath
			if movePath == "folder" {
				rel = "folder/кадр.jpg"
				if err := os.Mkdir(filepath.Join(origin, movePath), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(origin, rel)
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, time.Time{}, time.Unix(1700000000, 123456789)); err != nil {
				t.Fatal(err)
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			receipt := uploadReceipt{Source: "T", Path: rel, Size: st.Size(), Modified: st.ModTime().UnixNano()}
			if !receiptCurrent(receipt) {
				t.Fatal("fixture receipt was not current before the move")
			}
			req := httptest.NewRequest(http.MethodPost, "/api/move?source=T&path="+movePath+"&destSource=TARGET", nil)
			res := httptest.NewRecorder()
			handleMove(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("move status = %d, body = %s", res.Code, res.Body)
			}
			if !receiptCurrent(receipt) {
				t.Fatal("upload receipt stopped resolving to its original revision after the move")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old path stat error = %v, want path absent", err)
			}
			copied, err := os.ReadFile(filepath.Join(destination, rel))
			if err != nil || string(copied) != original {
				t.Fatalf("destination contents = %q, error = %v", copied, err)
			}
		})
	}
}
