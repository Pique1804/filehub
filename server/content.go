// Full-text + OCR content search. Extracts text from documents (pdftotext /
// LibreOffice) and from images (tesseract OCR), indexes it into a second FTS5
// table, and serves /api/search/content. Everything shells out to external CLIs
// (no cgo, single static binary preserved) and runs as a single throttled
// background lane so it never starves live file serving on the 2-core/2 GB CT.
package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxBodyBytes = 256 << 10     // stored body cap per file
const maxOCRImageBytes = 128 << 20 // original input cap; OCR receives only the 2000px copy

// ---- external tools (feature-detected once; missing => that lane no-ops) ----
func lookPath(b string) string { p, _ := exec.LookPath(b); return p }

var (
	pdftotextPath     = lookPath("pdftotext")
	pdftoppmPath      = lookPath("pdftoppm")
	tesseractPath     = lookPath("tesseract")
	vipsthumbnailPath = lookPath("vipsthumbnail")
	ionicePath        = lookPath("ionice")
	nicePath          = lookPath("nice")
	tessLangs         = detectTessLangs() // "rus+eng" / "eng" / "" if tesseract absent
)

// detectTessLangs picks rus+eng if both traineddata are present, else eng, else "".
func detectTessLangs() string {
	if tesseractPath == "" {
		return ""
	}
	out, err := exec.Command(tesseractPath, "--list-langs").CombinedOutput()
	if err != nil {
		return "eng"
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	switch {
	case have["rus"] && have["eng"]:
		return "rus+eng"
	case have["eng"]:
		return "eng"
	case have["rus"]:
		return "rus"
	}
	return "eng"
}

// ---- extension families ----
var plainExt = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".tsv": true, ".log": true,
	".json": true, ".ndjson": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".cfg": true, ".conf": true, ".xml": true, ".html": true, ".htm": true, ".srt": true,
	".vtt": true, ".rst": true, ".tex": true, ".go": true, ".py": true, ".js": true, ".ts": true,
	".jsx": true, ".tsx": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true, ".java": true,
	".kt": true, ".rs": true, ".rb": true, ".php": true, ".sh": true, ".sql": true, ".css": true,
	".scss": true, ".swift": true, ".lua": true, ".pl": true, ".r": true,
}
var ocrImageExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".heic": true, ".heif": true,
	".bmp": true, ".gif": true,
}

func isDocText(name string) bool {
	e := strings.ToLower(filepath.Ext(name))
	return plainExt[e] || e == ".pdf" || isOffice(name)
}
func isOCRImage(name string) bool { return ocrImageExt[strings.ToLower(filepath.Ext(name))] }

// contentSkipFile hides secret-ish / hidden files from the text index (their
// plaintext would otherwise land in index.db). Mirrors the hidden-dir skip intent.
func contentSkipFile(name, path string) bool {
	base := strings.ToLower(name)
	if strings.HasPrefix(base, ".") {
		return true
	}
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	if base == "id_rsa" || base == "id_dsa" || base == "id_ecdsa" {
		return true
	}
	for _, s := range []string{".key", ".pem", ".kdbx", ".p12", ".pfx"} {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return false
}

// ---- schema ----
func initContent() error {
	if db == nil {
		return nil
	}
	// OCR temp dirs live under previewDir; convertToPDF is the only other creator and
	// it only runs for office docs — without this, OCR on a box with no office files
	// would fail to create its temp dir and permanently record images as "no text".
	os.MkdirAll(previewDir, 0o755)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS content USING fts5(
		body, source UNINDEXED, path UNINDEXED, name UNINDEXED, mtime UNINDEXED,
		tokenize='unicode61 remove_diacritics 2');`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS content_meta(
		source TEXT NOT NULL, path TEXT NOT NULL,
		size INTEGER NOT NULL, mtime INTEGER NOT NULL,
		method TEXT, chars INTEGER, status INTEGER NOT NULL DEFAULT 0,
		rowid_fts INTEGER, done_at INTEGER,
		PRIMARY KEY(source,path));`); err != nil {
		return err
	}
	for _, col := range []string{"attempts INTEGER NOT NULL DEFAULT 0", "retry_after INTEGER NOT NULL DEFAULT 0"} {
		name := strings.Fields(col)[0]
		rows, e := db.Query(`PRAGMA table_info(content_meta)`)
		if e != nil {
			return e
		}
		found := false
		for rows.Next() {
			var cid, nn, pk int
			var n, t string
			var def any
			if rows.Scan(&cid, &n, &t, &nn, &def, &pk) == nil && n == name {
				found = true
			}
		}
		rows.Close()
		if !found {
			if _, e = db.Exec(`ALTER TABLE content_meta ADD COLUMN ` + col); e != nil {
				return e
			}
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS content_meta_status ON content_meta(status);`); err != nil {
		return err
	}
	// Earlier releases skipped legitimate high-resolution phone photos above 25 MB.
	// Reconsider those entries once; successful processing changes status from 2.
	_, err := db.Exec(`UPDATE content_meta SET attempts=0,retry_after=0 WHERE method='ocr' AND status=2 AND size<=?`, maxOCRImageBytes)
	return err
}

// ---- child runner: ionice -c3 nice -n19 <tool> (one process via exec-chain, so
// ctx-kill still hits the real tool); OMP single-thread; dies if filehub dies ----
func niced(ctx context.Context, name string, args ...string) *exec.Cmd {
	var c *exec.Cmd
	switch {
	case ionicePath != "" && nicePath != "":
		c = exec.CommandContext(ctx, ionicePath, append([]string{"-c3", nicePath, "-n", "19", name}, args...)...)
	case nicePath != "":
		c = exec.CommandContext(ctx, nicePath, append([]string{"-n", "19", name}, args...)...)
	default:
		c = exec.CommandContext(ctx, name, args...)
	}
	c.Env = append(os.Environ(), "OMP_THREAD_LIMIT=1")
	c.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return c
}

// capWriter buffers up to max bytes, discarding the rest while still draining the
// pipe (returns len(p)) so a pathological tool output can't OOM the box.
type capWriter struct {
	b   []byte
	max int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if len(c.b) < c.max {
		n := c.max - len(c.b)
		if n > len(p) {
			n = len(p)
		}
		c.b = append(c.b, p[:n]...)
	}
	return len(p), nil
}

func runCapped(cmd *exec.Cmd, max int) string {
	cw := &capWriter{max: max}
	cmd.Stdout = cw
	cmd.Stderr = io.Discard
	cmd.Run()
	return string(cw.b)
}

// ---- per-type extractors ----

var cp1251hi = [64]rune{
	0x0402, 0x0403, 0x201A, 0x0453, 0x201E, 0x2026, 0x2020, 0x2021, 0x20AC, 0x2030, 0x0409, 0x2039, 0x040A, 0x040C, 0x040B, 0x040F,
	0x0452, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0xFFFD, 0x2122, 0x0459, 0x203A, 0x045A, 0x045C, 0x045B, 0x045F,
	0x00A0, 0x040E, 0x045E, 0x0408, 0x00A4, 0x0490, 0x00A6, 0x00A7, 0x0401, 0x00A9, 0x0404, 0x00AB, 0x00AC, 0x00AD, 0x00AE, 0x0407,
	0x00B0, 0x00B1, 0x0406, 0x0456, 0x0491, 0x00B5, 0x00B6, 0x00B7, 0x0451, 0x2116, 0x0454, 0x00BB, 0x0458, 0x0405, 0x0455, 0x0457,
}

func decodeCP1251(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c < 0xC0:
			sb.WriteRune(cp1251hi[c-0x80])
		default:
			sb.WriteRune(0x0410 + rune(c-0xC0))
		}
	}
	return sb.String()
}

func extractPlain(full string) (string, int) {
	f, err := os.Open(full)
	if err != nil {
		return "", 3
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBodyBytes))
	if err != nil {
		return "", 3
	}
	nul := 0
	for _, b := range data {
		if b == 0 {
			nul++
		}
	}
	if len(data) > 0 && nul*100/len(data) > 1 { // NUL-heavy => binary, not text
		return "", 1
	}
	// The byte-cap can slice the final UTF-8 rune in half, which would make a valid
	// UTF-8 file look invalid and get misdecoded as CP1251. If trimming up to 3 tail
	// bytes restores validity, it was a cut rune; otherwise it's genuinely CP1251.
	if !utf8.Valid(data) {
		for k := 1; k <= 3 && k < len(data); k++ {
			if utf8.Valid(data[:len(data)-k]) {
				data = data[:len(data)-k]
				break
			}
		}
	}
	var s string
	if utf8.Valid(data) {
		s = string(data)
	} else {
		s = decodeCP1251(data)
	}
	s = strings.TrimSpace(s)
	if len([]rune(s)) < 2 {
		return "", 1
	}
	return s, 0
}

func pdftotextOf(ctx context.Context, pdf string) string {
	if pdftotextPath == "" {
		return ""
	}
	cmd := niced(ctx, pdftotextPath, "-q", "-enc", "UTF-8", "-l", "20", "-nopgbrk", "--", pdf, "-")
	return runCapped(cmd, 2<<20)
}

// ocrImage downscales with vips (also decodes HEIC, which tesseract can't) then OCRs.
func ocrImage(ctx context.Context, full string) (string, int) {
	if tessLangs == "" {
		return "", 3
	}
	tmp, err := os.MkdirTemp(previewDir, "ocr")
	if err != nil {
		return "", 3
	}
	defer os.RemoveAll(tmp)
	// Always OCR the vips-downscaled copy, never the original: downscale-first is the
	// pixel-count bound (a small file can decode to gigapixels and OOM tesseract). If
	// the downscale fails we skip rather than feed tesseract an unbounded image.
	if vipsthumbnailPath == "" {
		return "", 3
	}
	dst := filepath.Join(tmp, "o.png")
	vc := niced(ctx, vipsthumbnailPath, full, "-s", "2000x2000", "-o", dst+"[strip]")
	vc.Env = append(vc.Env, "VIPS_CONCURRENCY=1")
	if vc.Run() != nil {
		return "", 3
	}
	if st, e := os.Stat(dst); e != nil || st.Size() == 0 {
		return "", 3
	}
	cmd := niced(ctx, tesseractPath, dst, "stdout", "-l", tessLangs, "--oem", "1", "--psm", "3", "-c", "tessedit_do_invert=0")
	cw := &capWriter{max: 2 << 20}
	cmd.Stdout = cw
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return "", 3
	}
	return string(cw.b), 0
}

// ocrPDF rasterizes the first pages of a scanned PDF and OCRs them.
func ocrPDF(ctx context.Context, full string) string {
	if pdftoppmPath == "" || tessLangs == "" {
		return ""
	}
	tmp, err := os.MkdirTemp(previewDir, "pocr")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(tmp)
	// -scale-to caps the LONGER page dimension to 2000 px regardless of the PDF's
	// MediaBox, so a crafted huge-page PDF can't rasterize to a multi-GB bitmap and
	// OOM the box (mirrors ocrImage's vips bound; overrides -r).
	pc := niced(ctx, pdftoppmPath, "-png", "-scale-to", "2000", "-f", "1", "-l", "3", "--", full, filepath.Join(tmp, "p"))
	if pc.Run() != nil {
		return ""
	}
	pages, _ := filepath.Glob(filepath.Join(tmp, "p*.png"))
	var sb strings.Builder
	for _, p := range pages {
		cmd := niced(ctx, tesseractPath, p, "stdout", "-l", tessLangs, "--oem", "1", "--psm", "3", "-c", "tessedit_do_invert=0")
		sb.WriteString(runCapped(cmd, 1<<20))
		sb.WriteByte('\n')
		if sb.Len() > maxBodyBytes {
			break
		}
	}
	return sb.String()
}

// extractText dispatches by extension. Returns (body, method, status):
// status 0=ok 1=empty/no-text 2=too-big 3=error.
func extractText(ctx context.Context, full, name string, st os.FileInfo) (string, string, int) {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case ext == ".csv" || plainExt[ext]:
		s, status := extractPlain(full)
		return s, "text", status

	case ext == ".pdf":
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		s := strings.TrimSpace(pdftotextOf(cctx, full))
		cancel()
		if len(strings.ReplaceAll(s, " ", "")) < 16 { // image-only / scanned PDF
			octx, ocancel := context.WithTimeout(ctx, 60*time.Second)
			o := strings.TrimSpace(ocrPDF(octx, full))
			ocancel()
			if len([]rune(o)) >= 3 {
				return clipBody(o), "pdf-ocr", 0
			}
			return "", "pdf", 1
		}
		return clipBody(s), "pdf", 0

	case isOffice(name):
		pdf, err := convertToPDF(ctx, full, st)
		if err != nil {
			if errors.Is(err, errEmptySpreadsheet) {
				return "", "office", 1
			}
			if st.Size() > maxConvertBytes {
				return "", "office", 2
			}
			return "", "office", 3
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		s := strings.TrimSpace(pdftotextOf(cctx, pdf))
		cancel()
		if len([]rune(s)) < 2 {
			return "", "office", 1
		}
		return clipBody(s), "office", 0

	case ocrImageExt[ext]:
		if tessLangs == "" {
			return "", "none", 1
		}
		if st.Size() > maxOCRImageBytes {
			return "", "ocr", 2
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		body, ocrStatus := ocrImage(cctx, full)
		s := strings.TrimSpace(strings.ReplaceAll(body, "\f", ""))
		cancel()
		if ocrStatus != 0 {
			return "", "ocr", ocrStatus
		}
		if len([]rune(s)) < 3 {
			return "", "ocr", 1
		}
		return clipBody(s), "ocr", 0
	}
	return "", "none", 1
}

func clipBody(s string) string {
	if len(s) <= maxBodyBytes {
		return s
	}
	s = s[:maxBodyBytes]
	for !utf8.ValidString(s) && len(s) > 0 { // don't cut a multibyte rune in half
		s = s[:len(s)-1]
	}
	return s
}

// ---- index one file (skip-unchanged, one transaction) ----
func indexContent(source, path, full string, st os.FileInfo) {
	if db == nil {
		return
	}
	size, mtime := st.Size(), st.ModTime().Unix()
	var oldSize, oldMtime, retryAfter int64
	var oldStatus, attempts int
	err := db.QueryRow(`SELECT size,mtime,status,attempts,retry_after FROM content_meta WHERE source=? AND path=?`, source, path).Scan(&oldSize, &oldMtime, &oldStatus, &attempts, &retryAfter)
	same := err == nil && oldSize == size && oldMtime == mtime
	if same && (oldStatus < 2 || attempts >= 5 || retryAfter > time.Now().Unix()) {
		return
	}
	if !same {
		attempts = 0
	}
	contentSem <- struct{}{}
	body, method, status := extractText(context.Background(), full, filepath.Base(full), st)
	<-contentSem
	after, e := os.Stat(full)
	if e != nil || after.Size() != size || after.ModTime() != st.ModTime() {
		return
	}
	dbWriteMu.Lock()
	defer dbWriteMu.Unlock()
	tx, e := db.Begin()
	if e != nil {
		logDBError("content begin", e)
		return
	}
	defer tx.Rollback()
	var oldRowid sql.NullInt64
	e = tx.QueryRow(`SELECT rowid_fts FROM content_meta WHERE source=? AND path=?`, source, path).Scan(&oldRowid)
	if e != nil && e != sql.ErrNoRows {
		logDBError("content read", e)
		return
	}
	if oldRowid.Valid {
		if _, e = tx.Exec(`DELETE FROM content WHERE rowid=?`, oldRowid.Int64); e != nil {
			logDBError("content delete", e)
			return
		}
	}
	var newRowid sql.NullInt64
	if status == 0 && body != "" {
		res, err := tx.Exec(`INSERT INTO content(body,source,path,name,mtime) VALUES(?,?,?,?,?)`, body, source, path, filepath.Base(path), mtime)
		if err != nil {
			logDBError("content insert", err)
			return
		}
		id, err := res.LastInsertId()
		if err != nil {
			return
		}
		newRowid = sql.NullInt64{Int64: id, Valid: true}
	}
	next := int64(0)
	if status >= 2 {
		attempts++
		next = time.Now().Add(time.Duration(attempts) * 10 * time.Minute).Unix()
	}
	_, e = tx.Exec(`INSERT INTO content_meta(source,path,size,mtime,method,chars,status,rowid_fts,done_at,attempts,retry_after) VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(source,path) DO UPDATE SET size=excluded.size,mtime=excluded.mtime,method=excluded.method,chars=excluded.chars,status=excluded.status,rowid_fts=excluded.rowid_fts,done_at=excluded.done_at,attempts=excluded.attempts,retry_after=excluded.retry_after`, source, path, size, mtime, method, len([]rune(body)), status, newRowid, time.Now().Unix(), attempts, next)
	if e != nil {
		logDBError("content metadata", e)
		return
	}
	if e = tx.Commit(); e != nil {
		logDBError("content commit", e)
		return
	}
	if method == "ocr" || method == "pdf-ocr" {
		switch status {
		case 0:
			atomic.AddInt64(&ctDocsOCR, 1)
		case 1:
			atomic.AddInt64(&ctOCREmpty, 1)
		default:
			atomic.AddInt64(&ctOCRErr, 1)
		}
	} else {
		atomic.AddInt64(&ctDocsDone, 1)
	}
}

// indexOneFile text/OCR-indexes a single just-uploaded file immediately, without
// waiting for the (possibly hours-long, single-flighted) background sweep to finish.
func indexOneFile(source, rel string) {
	if db == nil {
		return
	}
	if !needsContent(source, rel) {
		return
	}
	full, ok := safePath(source, rel)
	if !ok {
		return
	}
	fi, err := os.Lstat(full)
	if err != nil || fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return
	}
	if !actualVideoFile(full) {
		indexContent(source, rel, full, fi)
	}
}

// contentDelete drops a file (or subtree) from the content index — wired into indexDelete.
func contentDelete(source, rel string) {
	if db == nil || rel == "" {
		return
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(rel)
	db.Exec(`DELETE FROM content WHERE rowid IN (
		SELECT rowid_fts FROM content_meta
		WHERE source=? AND (path=? OR path LIKE ? ESCAPE '\') AND rowid_fts IS NOT NULL)`,
		source, rel, esc+"/%")
	db.Exec(`DELETE FROM content_meta WHERE source=? AND (path=? OR path LIKE ? ESCAPE '\')`,
		source, rel, esc+"/%")
}

// ---- background sweep ----
var (
	contentSem  = make(chan struct{}, 1)
	contentBusy int32
	ctDocsDone  int64
	ctDocsOCR   int64
	ctOCREmpty  int64
	ctOCRErr    int64
	ctLane      atomic.Value // string
)

type pend struct {
	name, path, source string
	size               int64
}

// ocrMinBytes: don't waste OCR cycles on tiny images (icons, sprites, UI assets) —
// real screenshots/photos with searchable text are comfortably larger.
const ocrMinBytes = 20 << 10

func atoiEnv(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

func memAvailableMB() int {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 99999
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.Atoi(f[1])
				return kb / 1024
			}
		}
	}
	return 99999
}

func loadavg1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) > 0 {
		v, _ := strconv.ParseFloat(f[0], 64)
		return v
	}
	return 0
}

// waitRunnable backs off while the CT is low on RAM. NOTE: /proc/loadavg inside an
// unprivileged LXC reflects the *host's* load (lxcfs virtualizes meminfo but not
// loadavg here), so a load gate would misfire and park the sweep forever. We rely
// instead on the inherent brakes — one child at a time (contentSem), one core
// (OMP_THREAD_LIMIT=1), lowest priority (nice -19 ionice -c3), and the duty-factor
// cooldown — which keep extraction invisible to live serving. MemAvailable IS
// CT-scoped (lxcfs), so the RAM floor is the one valid governor.
func waitRunnable() {
	for {
		// Yield entirely to the thumbnail backfill — OCR + prewarm both hammer CPU/RAM,
		// and thumbnails are what the user is waiting on. (prewarmBusy is set for the
		// whole prewarmAll sweep.)
		if atomic.LoadInt32(&prewarmBusy) == 1 {
			time.Sleep(2 * time.Second)
			continue
		}
		if memAvailableMB() < 250 {
			time.Sleep(5 * time.Second)
			continue
		}
		return
	}
}

// pendingFiles returns index rows whose content_meta is missing or stale, filtered
// to the requested lane (docs vs OCR images), skipping noise/secret files.
func pendingFiles(ocr bool, limit int) []pend {
	if db == nil {
		return nil
	}
	q := `SELECT f.name,f.path,f.source,f.size FROM files f
		LEFT JOIN content_meta m ON m.source=f.source AND m.path=f.path
		WHERE f.isdir=0 AND (m.path IS NULL OR m.size<>f.size OR m.mtime<>f.mtime OR (m.status>=2 AND m.attempts<5 AND m.retry_after<=` + strconv.FormatInt(time.Now().Unix(), 10) + `))`
	if ocr {
		q += ` AND f.size > ` + strconv.Itoa(ocrMinBytes) +
			` ORDER BY (f.name LIKE 'Screenshot%' OR f.name LIKE 'Screen Shot%' OR f.path LIKE '%creenshot%') DESC, f.mtime DESC`
	}
	rows, err := db.Query(q)
	if err != nil {
		return nil
	}
	defer rows.Close()
	camera := atoiEnv("FILEHUB_OCR_CAMERA", 1) != 0
	types := mediaTypeMap()
	var out []pend
	for rows.Next() {
		var p pend
		if rows.Scan(&p.name, &p.path, &p.source, &p.size) != nil {
			continue
		}
		if photoNoise(p.path) || contentSkipFile(p.name, p.path) {
			continue
		}
		if ocr {
			if types[p.source+"|"+p.path] || !isOCRImage(p.name) {
				continue
			}
			if !inPhotoScope(p.source, p.path) { // OCR only the user's photo folder(s)
				continue
			}
			if !camera && !isScreenshot(p.name, p.path) {
				continue
			}
		} else if !isDocText(p.name) || isOCRImage(p.name) {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func isScreenshot(name, path string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "screenshot") || strings.HasPrefix(n, "screen shot") ||
		strings.Contains(strings.ToLower(path), "creenshot")
}

func processOne(p pend) time.Duration {
	full, ok := safePath(p.source, p.path)
	if !ok {
		return 0
	}
	fi, err := os.Lstat(full)
	if err != nil || fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 { // skip symlinks (P0)
		return 0
	}
	t0 := time.Now()
	indexContent(p.source, p.path, full, fi)
	return time.Since(t0)
}

func contentSweep() {
	if db == nil || !atomic.CompareAndSwapInt32(&contentBusy, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&contentBusy, 0)

	ctLane.Store("docs")
	for _, p := range pendingFiles(false, 0) {
		waitRunnable()
		processOne(p)
	}
	if tessLangs == "" || vipsthumbnailPath == "" {
		ctLane.Store("idle")
		return
	}
	ctLane.Store("ocr")
	for _, p := range pendingFiles(true, atoiEnv("FILEHUB_OCR_BUDGET", 12000)) {
		waitRunnable()
		d := processOne(p)
		time.Sleep(d) // duty-factor cooldown ≈ last child time => ~50% one-core duty
	}
	ctLane.Store("idle")
}

// ---- search ----
func buildContentMatch(q string) string {
	var toks []string
	for _, t := range strings.Fields(q) {
		t = strings.ReplaceAll(t, `"`, `""`)
		toks = append(toks, `"`+t+`"*`)
	}
	return strings.Join(toks, " ")
}

func handleContentSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	type contentHit struct {
		Source  string `json:"source"`
		Path    string `json:"path"`
		Name    string `json:"name"`
		ModTime int64  `json:"modTime"`
		Excerpt string `json:"excerpt"`
		Owner   string `json:"owner,omitempty"`
	}
	out := []contentHit{}
	if len([]rune(q)) < 2 || len(strings.Fields(q)) == 0 {
		writeJSON(w, map[string]any{"results": out})
		return
	}
	scope, args := requestFolders(r.Context()).sqlCondition("source", "path")
	rows, err := db.QueryContext(r.Context(), `SELECT source,path,name,mtime,snippet(content,0,'[',']','…',12)
		FROM content WHERE content MATCH ?`+scope+` ORDER BY bm25(content) LIMIT 200`, append([]any{buildContentMatch(q)}, args...)...)
	if err != nil {
		writeJSON(w, map[string]any{"results": out, "indexing": atomic.LoadInt32(&contentBusy) == 1})
		return
	}
	var keys []ownerPath
	for rows.Next() {
		var item contentHit
		if rows.Scan(&item.Source, &item.Path, &item.Name, &item.ModTime, &item.Excerpt) == nil {
			keys = append(keys, ownerPath{item.Source, item.Path})
			out = append(out, item)
		}
	}
	rows.Close()
	owners := fileOwnerLabels(r.Context(), keys, false)
	for i, key := range keys {
		out[i].Owner = owners[key]
	}
	writeJSON(w, map[string]any{"results": out, "indexing": atomic.LoadInt32(&contentBusy) == 1})
}

func handleContentReindex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("force") == "1" && db != nil {
		db.Exec(`DELETE FROM content`)
		db.Exec(`DELETE FROM content_meta`)
	}
	go contentSweep()
	writeJSON(w, map[string]any{"ok": true})
}

func handleContentStatus(w http.ResponseWriter, r *http.Request) {
	lane, _ := ctLane.Load().(string)
	avail := map[string]bool{
		"ocr":     tessLangs != "" && vipsthumbnailPath != "",
		"pdftext": pdftotextPath != "",
		"office":  sofficePath != "" && pdftotextPath != "",
	}
	stats := ocrLibraryStats()
	resp := map[string]any{
		"available": avail, "langs": tessLangs,
		"running": atomic.LoadInt32(&contentBusy) == 1, "lane": lane,
		"docsDone": atomic.LoadInt64(&ctDocsDone),
		"ocr": map[string]any{
			"done": stats["done"], "empty": stats["empty"],
			"errors": stats["errors"], "budget": atoiEnv("FILEHUB_OCR_BUDGET", 12000),
			"pending": stats["pending"], "eligible": stats["eligible"], "retrying": stats["retrying"],
		},
		"load1": loadavg1(), "memAvailMB": memAvailableMB(),
	}
	if !avail["ocr"] || !avail["pdftext"] {
		resp["install"] = "apt install tesseract-ocr tesseract-ocr-rus tesseract-ocr-eng libvips-tools poppler-utils"
	}
	writeJSON(w, resp)
}

func ocrLibraryStats() map[string]int {
	out := map[string]int{"done": 0, "empty": 0, "errors": 0, "pending": 0, "eligible": 0, "retrying": 0}
	if db == nil {
		return out
	}
	rows, e := db.Query(`SELECT f.name,f.path,f.source,f.size,f.mtime,coalesce(m.size,-1),coalesce(m.mtime,-1),coalesce(m.status,-1),coalesce(m.attempts,0) FROM files f LEFT JOIN content_meta m ON m.source=f.source AND m.path=f.path WHERE f.isdir=0 AND f.size>?`, ocrMinBytes)
	if e != nil {
		return out
	}
	defer rows.Close()
	camera := atoiEnv("FILEHUB_OCR_CAMERA", 1) != 0
	types := mediaTypeMap()
	for rows.Next() {
		var name, path, src string
		var size, mt, ms, mmt int64
		var status, tries int
		if rows.Scan(&name, &path, &src, &size, &mt, &ms, &mmt, &status, &tries) != nil {
			continue
		}
		if types[src+"|"+path] || !isOCRImage(name) || !inPhotoScope(src, path) || photoNoise(path) || contentSkipFile(name, path) || (!camera && !isScreenshot(name, path)) {
			continue
		}
		out["eligible"]++
		if ms != size || mt != mmt {
			out["pending"]++
		} else {
			switch status {
			case 0:
				out["done"]++
			case 1:
				out["empty"]++
			default:
				out["errors"]++
				if tries < 5 {
					out["retrying"]++
				}
			}
		}
	}
	return out
}

func needsContent(source, rel string) bool {
	name := filepath.Base(rel)
	return !photoNoise(rel) && !contentSkipFile(name, rel) && (isDocText(name) || isOCRImage(name) && inPhotoScope(source, rel))
}
