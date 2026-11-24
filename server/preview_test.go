package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePreviewWorkbook(t *testing.T, worksheet string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "example.xlsx")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	book := zip.NewWriter(file)
	for part, xml := range map[string]string{
		"xl/workbook.xml":          `<workbook><sheets><sheet name="Sheet1" sheetId="1"/></sheets></workbook>`,
		"xl/worksheets/sheet1.xml": `<worksheet>` + worksheet + `</worksheet>`,
	} {
		writer, err := book.Create(part)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(xml)); err != nil {
			t.Fatal(err)
		}
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestEmptyXLSXDoesNotMistakeCellsOrDrawingsForAnEmptySheet(t *testing.T) {
	for _, test := range []struct {
		worksheet string
		wantEmpty bool
	}{
		{`<sheetData/>`, true},
		{`<sheetData><row r="1"><c r="A1"><v>27</v></c></row></sheetData>`, false},
		{`<sheetData/><drawing id="drawing"/>`, false},
	} {
		file := writePreviewWorkbook(t, test.worksheet)
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := emptyXLSX(file, info.Size()); got != test.wantEmpty {
			t.Fatalf("emptyXLSX for %s = %v, want %v", test.worksheet, got, test.wantEmpty)
		}
	}
}

func TestSpreadsheetPreviewLimitsExportAndIgnoresOldCache(t *testing.T) {
	previousCache, previousConverter := previewDir, sofficePath
	t.Cleanup(func() { previewDir, sofficePath = previousCache, previousConverter })
	previewDir = filepath.Join(t.TempDir(), "preview cache")
	if err := os.Mkdir(previewDir, 0700); err != nil {
		t.Fatal(err)
	}
	sofficePath = filepath.Join(t.TempDir(), "soffice")
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("FILEHUB_PREVIEW_TEST_ARGS", argsPath)
	program := `#!/bin/sh
printf '%s\n' "$@" > "$FILEHUB_PREVIEW_TEST_ARGS"
while [ "$#" -gt 0 ]; do
  if [ "$1" = --outdir ]; then
    printf 'new preview\n' > "$2/example.pdf"
    exit 0
  fi
  shift
done
exit 1
`
	if err := os.WriteFile(sofficePath, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	full := writePreviewWorkbook(t, `<sheetData><row r="1"><c r="A1"><v>27</v></c></row></sheetData>`)
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", full, info.ModTime().UnixNano(), info.Size())))
	old := filepath.Join(previewDir, fmt.Sprintf("%x.pdf", oldHash))
	if err := os.WriteFile(old, []byte("old full conversion"), 0600); err != nil {
		t.Fatal(err)
	}
	pdf, err := convertToPDF(context.Background(), full, info)
	if err != nil || pdf == old {
		t.Fatalf("spreadsheet preview = %q, error = %v", pdf, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil || !strings.Contains(string(args), `pdf:calc_pdf_Export:{"PageRange":{"type":"string","value":"1-20"}}`) {
		t.Fatalf("unexpected converter options: %s, error = %v", args, err)
	}
	profile, _, _ := strings.Cut(string(args), "\n")
	if !strings.HasPrefix(profile, "-env:UserInstallation=file://") || !strings.Contains(profile, "/preview%20cache/") || !strings.HasSuffix(profile, "/libreoffice-profile") {
		t.Fatalf("unexpected converter profile: %q", profile)
	}
}
