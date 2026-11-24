// Document preview: PDFs are served as-is; Office docs (doc/xls/ppt/odt/...) are
// converted to PDF on demand via LibreOffice headless and disk-cached, so the
// Android app (and web) can render any document with a plain PDF viewer.
package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Office files bigger than this aren't converted (a huge spreadsheet could blow
// the 2-core/2 GB CT's RAM) — the app falls back to downloading the original.
const maxConvertBytes = 40 << 20 // 40 MiB
const maxSpreadsheetPDFBytes = 32 << 20
const spreadsheetPreviewPages = 20 // also the limit used by the text indexer

var errEmptySpreadsheet = errors.New("empty spreadsheet")
var errSpreadsheetPreviewTooLarge = errors.New("spreadsheet preview is too large")

var officeExt = map[string]bool{
	".doc": true, ".docx": true, ".odt": true, ".rtf": true,
	".xls": true, ".xlsx": true, ".ods": true, ".csv": true,
	".ppt": true, ".pptx": true, ".odp": true,
}

func isOffice(name string) bool { return officeExt[strings.ToLower(filepath.Ext(name))] }
func isPreviewable(name string) bool {
	return strings.ToLower(filepath.Ext(name)) == ".pdf" || isOffice(name)
}

func isSpreadsheet(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xls", ".xlsx", ".ods", ".csv":
		return true
	}
	return false
}

var sofficePath = func() string { p, _ := exec.LookPath("soffice"); return p }()
var previewDir = env("FILEHUB_PREVIEW_DIR", "/opt/filehub/previews")

// LibreOffice is RAM-heavy and a single profile can't run concurrent jobs safely,
// so conversions are serialized.
var previewSem = make(chan struct{}, 1)

func previewCachePath(full string, st os.FileInfo) string {
	descriptor := fmt.Sprintf("%s|%d|%d", full, st.ModTime().UnixNano(), st.Size())
	if isSpreadsheet(full) {
		descriptor += fmt.Sprintf("|calc-pages-%d", spreadsheetPreviewPages)
	}
	key := sha256.Sum256([]byte(descriptor))
	return filepath.Join(previewDir, hex.EncodeToString(key[:])+".pdf")
}

// Recognize small, definitively blank workbooks. Leave unfamiliar workbooks
// and sheets with cells or drawings to LibreOffice.
func emptyXLSX(full string, size int64) bool {
	if !strings.EqualFold(filepath.Ext(full), ".xlsx") || size > 256<<10 {
		return false
	}
	book, err := zip.OpenReader(full)
	if err != nil {
		return false
	}
	defer book.Close()
	hasWorkbook, hasSheet := false, false
	for _, part := range book.File {
		if part.Name == "xl/workbook.xml" {
			hasWorkbook = true
		}
		for _, prefix := range []string{"xl/drawings/", "xl/chartsheets/", "xl/pivotTables/", "xl/embeddings/"} {
			if strings.HasPrefix(part.Name, prefix) {
				return false
			}
		}
		if path.Dir(part.Name) == "xl/worksheets" && strings.HasSuffix(part.Name, ".xml") {
			hasSheet = true
			if !emptyWorksheet(part) {
				return false
			}
		}
	}
	return hasWorkbook && hasSheet
}

func emptyWorksheet(part *zip.File) bool {
	const maxXML = 64 << 10
	if part.UncompressedSize64 > maxXML {
		return false
	}
	file, err := part.Open()
	if err != nil {
		return false
	}
	defer file.Close()
	decoder := xml.NewDecoder(io.LimitReader(file, maxXML+1))
	hasSheetData := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return err == io.EOF && hasSheetData && decoder.InputOffset() <= maxXML
		}
		if element, ok := token.(xml.StartElement); ok {
			switch element.Name.Local {
			case "sheetData":
				hasSheetData = true
			case "c", "drawing", "legacyDrawing", "picture", "oleObjects", "controls", "extLst":
				return false
			}
		}
	}
}

// convertToPDF runs LibreOffice headless and returns the path to a cached PDF.
func convertToPDF(ctx context.Context, full string, st os.FileInfo) (string, error) {
	if emptyXLSX(full, st.Size()) {
		return "", errEmptySpreadsheet
	}
	cache := previewCachePath(full, st)
	if _, err := os.Stat(cache); err == nil {
		return cache, nil
	}
	if sofficePath == "" {
		return "", fmt.Errorf("no converter")
	}
	if st.Size() > maxConvertBytes {
		return "", fmt.Errorf("too large to preview")
	}
	os.MkdirAll(previewDir, 0o755)
	select {
	case previewSem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-previewSem }()
	if _, err := os.Stat(cache); err == nil { // someone converted while we waited
		return cache, nil
	}

	tmp, err := os.MkdirTemp(previewDir, "conv")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	// Hard timeout so a pathological file can't pin soffice (and RAM) forever.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	format := "pdf"
	if isSpreadsheet(full) {
		format = fmt.Sprintf(`pdf:calc_pdf_Export:{"PageRange":{"type":"string","value":"1-%d"}}`, spreadsheetPreviewPages)
	}
	produced, err := runOfficePDF(ctx, full, tmp, format)
	if err != nil {
		return "", err
	}
	if isSpreadsheet(full) {
		info, err := os.Stat(produced)
		if err != nil {
			return "", err
		}
		if info.Size() > maxSpreadsheetPDFBytes {
			return "", errSpreadsheetPreviewTooLarge
		}
	}
	if err := moveFile(produced, cache); err != nil {
		return "", err
	}
	return cache, nil
}

func runOfficePDF(ctx context.Context, full, tmp, format string) (string, error) {
	profilePath, err := filepath.Abs(filepath.Join(tmp, "libreoffice-profile"))
	if err != nil {
		return "", err
	}
	profile := (&url.URL{Scheme: "file", Path: profilePath}).String()
	// An explicit profile also works with numeric container users that have no passwd entry.
	cmd := exec.CommandContext(ctx, sofficePath, "-env:UserInstallation="+profile, "--headless", "--norestore", "--nolockcheck",
		"--convert-to", format, "--outdir", tmp, full)
	cmd.Env = append(os.Environ(), "HOME="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("convert: %v: %s", err, string(out))
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return "", err
	}
	// LibreOffice may sanitize the output filename.
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			return filepath.Join(tmp, entry.Name()), nil
		}
	}
	return "", fmt.Errorf("no pdf produced")
}

func handlePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	q := r.URL.Query()
	full, _, ok := resolveItem(q.Get("source"), q.Get("path"))
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(full))
	if ext == ".pdf" {
		http.ServeFile(w, r, full) // already a PDF; ServeFile adds range + content-type
		return
	}
	if isOffice(full) {
		pdf, err := convertToPDF(r.Context(), full, st)
		if err != nil {
			switch {
			case errors.Is(err, errEmptySpreadsheet):
				w.Header().Set("X-FileHub-Preview", "empty")
				http.Error(w, "empty spreadsheet", http.StatusUnprocessableEntity)
				return
			case st.Size() > maxConvertBytes || errors.Is(err, errSpreadsheetPreviewTooLarge):
				http.Error(w, "preview too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "preview failed", http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		if isSpreadsheet(full) {
			w.Header().Set("X-FileHub-Preview-Pages", fmt.Sprint(spreadsheetPreviewPages))
		}
		http.ServeFile(w, r, pdf)
		return
	}
	http.Error(w, "not previewable", http.StatusUnsupportedMediaType)
}
