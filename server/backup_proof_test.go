package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupProofUsesExactScopedBytesOnAnotherVolume(t *testing.T) {
	destination, err := os.MkdirTemp("/dev/shm", "filehub-proof-")
	if err != nil {
		t.Skipf("independent test volume unavailable: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(destination) })
	original := t.TempDir()
	oldSources := byName
	byName = map[string]string{"test": original}
	t.Cleanup(func() { byName = oldSources })

	destInfo, err := backupDirectoryInfo(destination)
	if err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := backupDirectoryInfo(original)
	if err != nil {
		t.Fatal(err)
	}
	if destInfo.Device == sourceInfo.Device {
		t.Skip("temporary source and destination use one volume")
	}

	plan := backupPlan{
		Instance: strings.Repeat("a", 32), Destination: destInfo,
		Folders: []backupResolvedFolder{{backupFolder: backupFolder{Source: "test", Path: "Camera"}}},
	}
	run := backupRun{State: "ok", Snapshot: "snapshot-20261121T150000123456Z"}
	copy := &backupCopy{Plan: plan, Run: run}
	path := filepath.Join(destination, managedBackupDirectory, plan.Instance, run.Snapshot)
	subdir := fmt.Sprintf("sources/%x/Camera", sha256.Sum256([]byte("test")))
	for _, parent := range []string{filepath.Join(path, subdir), filepath.Join(original, "Camera")} {
		if err := os.MkdirAll(parent, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "file.jpg"), []byte("exact-test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := openBackupSnapshot(copy)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	item := fileReceipt{Path: "Camera/file.jpg", Size: 10,
		SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("exact-test")))}
	check := func(receipt fileReceipt, want string) {
		t.Helper()
		if got := verifyBackupReceipt(context.Background(), root, plan, "test", receipt); got != want {
			t.Fatalf("proof for %s = %q, want %q", receipt.Path, got, want)
		}
	}
	check(item, "confirmed")
	outside := item
	outside.Path = "CameraOther/file.jpg"
	check(outside, "unconfirmed")
	bad := item
	bad.SHA256 = strings.Repeat("a", 64)
	check(bad, "unconfirmed")
	if err := os.WriteFile(filepath.Join(path, subdir, "file.jpg"), []byte("wrong-data"), 0600); err != nil {
		t.Fatal(err)
	}
	check(item, "unconfirmed")

	invalid := *copy
	invalid.Plan.Destination.Inode++
	if candidate, err := openBackupSnapshot(&invalid); err == nil {
		candidate.Close()
		t.Fatal("backup was opened after destination identity changed")
	}
}
