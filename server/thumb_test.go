package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestThumbnailStopsWaitingWhenRequestIsCanceled(t *testing.T) {
	directory := t.TempDir()
	previous := thumbDir
	thumbDir = directory
	t.Cleanup(func() { thumbDir = previous })
	original := filepath.Join(directory, "photo.jpg")
	if err := os.WriteFile(original, nil, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	imageThumbWorker <- struct{}{}
	defer func() { <-imageThumbWorker }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := genThumb(ctx, original, info, gridThumbW); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled thumbnail request, got %v", err)
	}
}
