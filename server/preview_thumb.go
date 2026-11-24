package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var documentThumbs = struct {
	mu    sync.Mutex
	gate  chan struct{}
	jpegs map[string][]byte
}{gate: make(chan struct{}, 1), jpegs: make(map[string][]byte)}

func cachedDocumentThumb(key string) []byte {
	documentThumbs.mu.Lock()
	defer documentThumbs.mu.Unlock()
	return documentThumbs.jpegs[key]
}

func genDocumentThumb(parent context.Context, full string, st os.FileInfo, size int) ([]byte, error) {
	if pdftoppmPath == "" || st.Size() > maxConvertBytes {
		return nil, fmt.Errorf("document thumbnail unavailable")
	}
	key := thumbCachePath(full, st, size)
	if data := cachedDocumentThumb(key); data != nil {
		return data, nil
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	select {
	case documentThumbs.gate <- struct{}{}:
		defer func() { <-documentThumbs.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if data := cachedDocumentThumb(key); data != nil {
		return data, nil
	}
	pdf := full
	if isOffice(full) {
		cached := previewCachePath(full, st)
		if _, err := os.Stat(cached); err == nil {
			pdf = cached
		} else {
			if sofficePath == "" || emptyXLSX(full, st.Size()) {
				return nil, fmt.Errorf("document thumbnail unavailable")
			}
			tmp, err := os.MkdirTemp("", "filehub-docthumb-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(tmp)
			select {
			case previewSem <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			pdf, err = runOfficePDF(ctx, full, tmp, firstPagePDFFormat(full))
			<-previewSem
			if err != nil {
				return nil, err
			}
		}
	}
	data, err := niced(ctx, pdftoppmPath, "-f", "1", "-l", "1", "-singlefile",
		"-scale-to", strconv.Itoa(size), "-jpeg", "--", pdf).Output()
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("document thumbnail failed")
	}
	// Keep the working set in memory, at most 8 MiB of JPEG data.
	if len(data) <= 64<<10 {
		documentThumbs.mu.Lock()
		if len(documentThumbs.jpegs) >= 128 {
			for cached := range documentThumbs.jpegs {
				delete(documentThumbs.jpegs, cached)
				break
			}
		}
		documentThumbs.jpegs[key] = data
		documentThumbs.mu.Unlock()
	}
	return data, nil
}

func firstPagePDFFormat(name string) string {
	filter := "writer_pdf_Export"
	if isSpreadsheet(name) {
		filter = "calc_pdf_Export"
	} else {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".odp", ".ppt", ".pptx":
			filter = "impress_pdf_Export"
		}
	}
	return fmt.Sprintf(`pdf:%s:{"PageRange":{"type":"string","value":"1"}}`, filter)
}
