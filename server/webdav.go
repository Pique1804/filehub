// WebDAV endpoint — mount filehub as a network drive (Windows Explorer, macOS
// Finder, Linux davfs2, rclone). A single /dav/ mount exposes a synthetic root
// that lists the sources (HDD1, HDD2) as top-level folders. Authentication is
// HTTP Basic against the users table (WebDAV clients do not send our session
// cookie), cached to avoid a bcrypt verify on every PROPFIND; the viewer role
// is read-only; every write keeps the FTS index fresh.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/webdav"
)

// davStart is a stable, non-zero ModTime for the synthetic root. (The zero
// time.Time is year 1, which some WebDAV clients reject.)
var davStart = time.Now()

// ---- path resolution ------------------------------------------------------

// davResolve maps a webdav path (always "/"-rooted, forward-slash, Prefix
// already stripped by webdav.Handler) to (source, rel, realOSPath).
//
//	"/"               -> source=="" (the synthetic root; rel=="", full=="")
//	"/HDD1"           -> source=="HDD1", rel=="" (the disk node; full==its root)
//	"/HDD1/a/b.txt"   -> source=="HDD1", rel=="a/b.txt", full==<root>/a/b.txt
//
// rel is forward-slash relative to the source root — exactly the shape the FTS
// index hooks expect. ok==false means unknown source OR traversal escape, which
// every method turns into os.ErrNotExist.
func davResolve(name string) (source, rel, full string, ok bool) {
	clean := path.Clean("/" + strings.TrimPrefix(name, "/")) // collapse . .. //
	if clean == "/" {
		return "", "", "", true // synthetic root
	}
	parts := strings.SplitN(strings.TrimPrefix(clean, "/"), "/", 2)
	source = parts[0]
	if len(parts) == 2 {
		rel = parts[1]
	}
	full, ok = safePath(source, rel) // REUSE: validates source exists + no escape
	if !ok {
		return "", "", "", false
	}
	return source, rel, full, true
}

// realInside reports whether full, after resolving symlinks, is still inside
// root. safePath only checks the *lexical* joined path; an in-tree symlink that
// points outside the source would pass safePath yet escape on disk. This mirrors
// the symlink revalidation the zip walker already does. On create the leaf does
// not exist yet, so callers pass the parent directory instead.
func realInside(root, full string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	for probe := full; ; {
		resolved, evalErr := filepath.EvalSymlinks(probe)
		if evalErr == nil {
			return resolved == resolvedRoot || strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator))
		}
		if !errors.Is(evalErr, fs.ErrNotExist) {
			return false
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return false
		}
		probe = parent
	}
}

// ---- multi-source webdav.FileSystem ---------------------------------------

// davFS is a synthetic-root webdav.FileSystem over the existing sources/byName.
// It holds no mutable state; every method derives everything from the request,
// the immutable source tables, and (for role) the request context set by the
// auth middleware.
type davFS struct {
	replacements map[davReplacementPath]*davReplacement
}

func (davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	source, rel, full, ok := davResolve(name)
	if !ok {
		return nil, os.ErrNotExist
	}
	if source == "" {
		return rootInfo{}, nil // synthetic "/"
	}
	if access := requestFolders(ctx); access != nil {
		full, ok = access.browsePath(source, rel)
		if !ok || !access.visibleEntry(source, rel) {
			return nil, os.ErrNotExist
		}
	}
	if !realInside(byName[source], full) {
		return nil, os.ErrPermission
	}
	return os.Stat(full)
}

func (fs davFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	source, rel, full, ok := davResolve(name)
	if !ok {
		return nil, os.ErrNotExist
	}
	writing := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if source == "" { // synthetic root: read-only listing of the sources
		if writing {
			return nil, os.ErrPermission
		}
		return &rootDir{entries: davSourceEntries(requestFolders(ctx)), info: rootInfo{}}, nil
	}
	if writing && (rel == "" || roleIsViewer(ctx) || !davCanWrite(ctx, source, rel)) {
		return nil, os.ErrPermission // viewer = read-only (defense in depth)
	}
	if !realInside(byName[source], full) {
		return nil, os.ErrPermission
	}
	if writing {
		transfer, _ := ctx.Value(davTransferKey{}).(*davTransfer)
		if transfer != nil && transfer.patch && flag == os.O_RDWR {
			// x/net/webdav uses O_RDWR to look for a DeadPropsHolder.
			// A read-only file lets it decline those properties individually.
			return os.Open(full)
		}
		if transfer == nil || transfer.input == nil {
			return nil, errors.New("DAV write requires tracked input")
		}
		var quotaCeiling *int64
		var reservation *quotaReservation
		if transfer.method == http.MethodPut {
			unlock := davMutationLock(ctx)
			var err error
			reservation, quotaCeiling, err = davPutQuota(ctx, full, transfer.input.expected)
			unlock()
			if err != nil {
				transfer.quotaErr = err
				return nil, err
			}
		}
		f, err := os.CreateTemp(filepath.Dir(full), ".filehub-dav-*")
		if err != nil {
			reservation.release()
			return nil, err
		}
		if err = f.Chmod(perm.Perm()); err != nil {
			reservation.release()
			f.Close()
			os.Remove(f.Name())
			return nil, err
		}
		file := &davFile{File: f, ctx: ctx, input: transfer.input, source: source, rel: rel, full: full, staged: f.Name(), fs: fs, transfer: transfer, quotaCeiling: quotaCeiling, reservation: reservation}
		if transfer.client != "" && transfer.method == http.MethodPut {
			unlock := davMutationLock(ctx)
			file.replacement = fs.finderReplacement(transfer.client, source, rel)
			unlock()
		}
		return file, nil
	}
	access := requestFolders(ctx)
	if access != nil {
		full, ok = access.browsePath(source, rel)
		if !ok {
			return nil, os.ErrNotExist
		}
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	if access != nil {
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if !info.IsDir() && !access.canRead(source, rel) {
			f.Close()
			return nil, os.ErrNotExist
		}
		if info.IsDir() {
			entries, err := f.Readdir(-1)
			f.Close()
			if err != nil {
				return nil, err
			}
			visible := make([]os.FileInfo, 0, len(entries))
			for _, entry := range entries {
				if access.visibleEntry(source, joinRel(rel, entry.Name())) {
					visible = append(visible, entry)
				}
			}
			return &rootDir{info: info, entries: visible}, nil
		}
	}
	// COPY reads one regular file and then opens its destination. Observe EOF
	// and read failures just as for a PUT request body; Close alone is not success.
	if transfer, _ := ctx.Value(davTransferKey{}).(*davTransfer); transfer != nil && transfer.copy {
		if st, e := f.Stat(); e == nil && st.Mode().IsRegular() {
			input := &davInput{ReadCloser: f, expected: st.Size()}
			transfer.input = input
			return &davReadFile{File: f, input: input}, nil
		}
	}
	return f, nil
}

func (davFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	source, rel, full, ok := davResolve(name)
	if !ok {
		return os.ErrNotExist
	}
	if source == "" { // can't create a "disk" at the synthetic root
		return os.ErrPermission
	}
	if requestFolders(ctx) != nil {
		unlock := davMutationLock(ctx)
		defer unlock()
	}
	if roleIsViewer(ctx) || !davCanWrite(ctx, source, rel) {
		return os.ErrPermission
	}
	if !realInside(byName[source], filepath.Dir(full)) {
		return os.ErrPermission
	}
	if err := os.Mkdir(full, perm); err != nil {
		return err
	}
	indexAddPath(source, full, rel)
	return nil
}

func (fs davFS) RemoveAll(ctx context.Context, name string) error {
	source, rel, _, ok := davResolve(name)
	if !ok {
		return os.ErrNotExist
	}
	if source == "" || rel == "" || roleIsViewer(ctx) {
		return os.ErrPermission
	}
	unlock := davMutationLock(ctx)
	defer unlock()
	if !davCanWrite(ctx, source, rel) {
		return os.ErrPermission
	}
	meta, err := deleteFileToTrashRecord(source, rel)
	if err == nil {
		key := davReplacementPath{source, rel}
		delete(fs.replacements, key)
		transfer, _ := ctx.Value(davTransferKey{}).(*davTransfer)
		if transfer != nil && transfer.method == http.MethodDelete && transfer.client != "" && !meta.IsDir && meta.Size > 0 {
			fs.rememberFinderDeletion(key, transfer.client, meta.ID)
		}
	}
	return err
}

func (davFS) Rename(ctx context.Context, oldName, newName string) error {
	osrc, orel, _, ok1 := davResolve(oldName)
	nsrc, nrel, _, ok2 := davResolve(newName)
	if !ok1 || !ok2 {
		return os.ErrNotExist
	}
	if osrc == "" || orel == "" || nsrc == "" || nrel == "" || roleIsViewer(ctx) {
		return os.ErrPermission
	}
	unlock := davMutationLock(ctx)
	defer unlock()
	if !davCanWrite(ctx, osrc, orel) || !davCanWrite(ctx, nsrc, nrel) {
		return os.ErrPermission
	}
	return moveFileReferences(osrc, orel, nsrc, nrel)
}

// x/net/webdav closes destinations even when io.Copy failed. Completion is
// therefore supplied by a tracked request body / COPY source, never by Close.
type davInput struct {
	io.ReadCloser
	expected, received int64
	complete           bool
	err                error
}

func (b *davInput) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.received += int64(n)
	if err == io.EOF {
		if b.expected >= 0 && b.received != b.expected {
			err = io.ErrUnexpectedEOF
		} else {
			b.complete = true
		}
	}
	if b.expected >= 0 && b.received > b.expected {
		err = errors.New("DAV body exceeds Content-Length")
	}
	if err != nil && err != io.EOF {
		b.err = err
	}
	return n, err
}

type davReadFile struct {
	webdav.File
	input *davInput
}

func (f *davReadFile) Read(p []byte) (int, error) { return f.input.Read(p) }

type davTransfer struct {
	input          *davInput
	copy           bool
	patch          bool
	client, method string
	quotaErr       error
}
type davTransferKey struct{}
type davMutationKey struct{}

func davMutationLock(ctx context.Context) func() {
	if held, _ := ctx.Value(davMutationKey{}).(bool); held {
		return func() {}
	}
	fileMutationMu.Lock()
	return fileMutationMu.Unlock
}
func davTransfers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// MOVE/COPY include a destination DELETE. Keep those steps serialized
		// with API edits; a slow network PUT only takes this lock at publication.
		if r.Method == "MOVE" || r.Method == "COPY" || r.Method == "DELETE" {
			fileMutationMu.Lock()
			defer fileMutationMu.Unlock()
			ctx = context.WithValue(ctx, davMutationKey{}, true)
		}
		transfer := &davTransfer{copy: r.Method == "COPY", patch: r.Method == "PROPPATCH", method: r.Method}
		if account, ok := ctx.Value(profileKey{}).(profileIdentity); ok && strings.HasPrefix(r.UserAgent(), "WebDAVFS/") {
			transfer.client = account.username + "\x00" + account.version + "\x00" + loginKey(r)
		}
		if r.Method == "PUT" {
			transfer.input = &davInput{ReadCloser: r.Body, expected: r.ContentLength}
			r.Body = transfer.input
		}
		ctx = context.WithValue(ctx, davTransferKey{}, transfer)
		if transfer.copy {
			if err := checkDAVCopyQuota(r.WithContext(ctx)); writeQuotaError(w, err) {
				return
			}
		}
		if transfer.copy || r.Method == http.MethodPut {
			w = &davQuotaResponse{ResponseWriter: w, transfer: transfer}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type davFile struct {
	*os.File
	ctx                       context.Context
	input                     *davInput
	source, rel, full, staged string
	once                      sync.Once
	result                    error
	writeErr                  error
	fs                        davFS
	replacement               *davReplacement
	transfer                  *davTransfer
	quotaCeiling              *int64
	reservation               *quotaReservation
	written                   int64
}

func (f *davFile) Write(p []byte) (int, error) {
	if f.quotaCeiling != nil && int64(len(p)) > *f.quotaCeiling-f.written {
		f.transfer.quotaErr = errPersonalQuota
		f.writeErr = errPersonalQuota
		return 0, errPersonalQuota
	}
	n, err := f.File.Write(p)
	f.written += int64(n)
	if err != nil {
		f.writeErr = err
	}
	if n != len(p) && err == nil {
		f.writeErr = io.ErrShortWrite
	}
	return n, err
}

// Hide os.File.ReadFrom: io.Copy must use Write so disk errors are recorded.
func (f *davFile) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{f}, r)
}
func (f *davFile) Close() error {
	f.once.Do(func() {
		defer os.Remove(f.staged)
		if f.result = f.ctx.Err(); f.result == nil {
			f.result = f.input.err
		}
		if f.result == nil {
			f.result = f.writeErr
		}
		if f.result == nil && !f.input.complete {
			f.result = io.ErrUnexpectedEOF
		}
		if f.result == nil {
			f.result = f.File.Sync()
		}
		if err := f.File.Close(); f.result == nil {
			f.result = err
		}
		if f.result != nil && f.replacement == nil && f.reservation == nil {
			return
		}
		unlock := davMutationLock(f.ctx)
		defer unlock()
		f.reservation.releaseLocked()
		if f.result != nil {
			f.recoverFinderReplacementLocked()
			return
		}
		if f.result = f.ctx.Err(); f.result != nil {
			f.recoverFinderReplacementLocked()
			return
		}
		if !davCanWrite(f.ctx, f.source, f.rel) {
			f.result = os.ErrPermission
			return
		}
		if full, ok := safePath(f.source, f.rel); !ok || full != f.full {
			f.result = os.ErrPermission
			return
		}
		st, err := os.Lstat(f.full)
		if err == nil || os.IsNotExist(err) {
			account, quotaErr := quotaAccount(f.ctx)
			if quotaErr == nil {
				var previous int64
				if st != nil && st.Mode().IsRegular() {
					previous = st.Size()
				}
				quotaErr = checkQuotaGrowth(f.ctx, account, f.written-previous, "")
			}
			if quotaErr != nil {
				f.transfer.quotaErr = quotaErr
				f.result = quotaErr
				f.recoverFinderReplacementLocked()
				return
			}
		}
		if err == nil {
			if !st.Mode().IsRegular() {
				f.result = os.ErrInvalid
				return
			}
			// Preserve permissions and the exact previous bytes before publishing.
			if f.result = os.Chmod(f.staged, st.Mode().Perm()); f.result != nil {
				return
			}
			if _, f.result = savePhotoVersion(f.source, f.rel, "webdav-put"); f.result != nil {
				return
			}
			f.result = os.Rename(f.staged, f.full)
		} else if os.IsNotExist(err) {
			f.result = renameNoReplace(f.staged, f.full)
		} else {
			f.result = err
		}
		if f.result != nil {
			return
		}
		f.observeFinderPublication(st, os.IsNotExist(err))
		if f.result = syncFile(filepath.Dir(f.full)); f.result != nil {
			return
		}
		f.result = refreshReplacedFile(f.source, f.rel, f.full)
	})
	return f.result
}

// ---- synthetic root ("/") that lists the sources as folders ---------------

type rootInfo struct{}

func (rootInfo) Name() string       { return "/" }
func (rootInfo) Size() int64        { return 0 }
func (rootInfo) Mode() os.FileMode  { return os.ModeDir | 0o555 }
func (rootInfo) ModTime() time.Time { return davStart }
func (rootInfo) IsDir() bool        { return true }
func (rootInfo) Sys() any           { return nil }

// renamedInfo presents a source's real dir info under its source name, so the
// top-level listing shows real Size()/ModTime() (Finder needs accurate UTC
// times and Content-Length).
type renamedInfo struct {
	os.FileInfo
	name string
}

func (r renamedInfo) Name() string { return r.name }

// rootDir is the read-only webdav.File for "/". Only Readdir/Stat do real work;
// byte Read/Seek error like a real directory so clients don't try to GET it.
type rootDir struct {
	off     int
	info    os.FileInfo
	entries []os.FileInfo
}

func (d *rootDir) Close() error                   { return nil }
func (d *rootDir) Read([]byte) (int, error)       { return 0, errors.New("is a directory") }
func (d *rootDir) Seek(int64, int) (int64, error) { return 0, errors.New("is a directory") }
func (d *rootDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (d *rootDir) Stat() (os.FileInfo, error)     { return d.info, nil }

// Readdir lists one entry per source in the stable order of `sources` (ranging
// the byName map would reorder the listing between PROPFINDs). count<=0 returns
// everything, the case webdav.Handler always uses.
func davSourceEntries(access *folderAccess) []os.FileInfo {
	all := make([]os.FileInfo, 0, len(sources))
	for _, s := range sources {
		if !access.canBrowse(s.Name, "") {
			continue
		}
		fi, err := os.Stat(s.Root)
		if err == nil {
			all = append(all, renamedInfo{fi, s.Name})
		}
	}
	return all
}

func (d *rootDir) Readdir(count int) ([]os.FileInfo, error) {
	all := d.entries
	if count <= 0 {
		d.off = len(all)
		return all, nil
	}
	if d.off >= len(all) {
		return nil, io.EOF
	}
	end := d.off + count
	if end > len(all) {
		end = len(all)
	}
	res := all[d.off:end]
	d.off = end
	return res, nil
}

// ---- role plumbing via context --------------------------------------------

type davCtxKey struct{}

func roleIsViewer(ctx context.Context) bool {
	r, _ := ctx.Value(davCtxKey{}).(string)
	return r == "viewer"
}

// ---- basic-auth (cached) + role enforcement -------------------------------

type davCacheEnt struct {
	role    string
	pwSum   [32]byte
	version string
	exp     time.Time
}

var davCache sync.Map // username -> davCacheEnt

// davInvalidate drops a user's cached WebDAV credentials so a delete / role
// change / password reset takes effect immediately instead of after the TTL.
func davInvalidate(username string) { davCache.Delete(username) }

// davDummyHash is a real, cost-matched bcrypt hash. On an unknown user we still
// run a bcrypt compare against it so the wrong-user path costs the same as the
// wrong-password path, closing the username-enumeration timing oracle.
var davDummyHash []byte

func init() {
	if h, err := bcrypt.GenerateFromPassword([]byte("filehub-dummy"), bcrypt.DefaultCost); err == nil {
		davDummyHash = h
	}
}

// davAuth verifies credentials, caching a successful verify for 5 min so the
// chatty PROPFIND storms a single folder-open produces don't each pay bcrypt.
func davCachedVersion(username string, sum [32]byte, version string) bool {
	if v, hit := davCache.Load(username); hit {
		e := v.(davCacheEnt)
		if time.Now().Before(e.exp) {
			if e.version == version && subtle.ConstantTimeCompare(sum[:], e.pwSum[:]) == 1 {
				e.exp = time.Now().Add(5 * time.Minute)
				davCache.Store(username, e)
				return true
			}
		} else {
			davCache.Delete(username) // expired: free the dead entry
		}
	}
	return false
}

func davAuthCached(username, password string) (string, bool) {
	var role, version string
	if db.QueryRow(`SELECT role,session_version FROM users WHERE username=?`, username).Scan(&role, &version) != nil {
		return "", false
	}
	return role, davCachedVersion(username, sha256.Sum256([]byte(password)), version)
}

func davAuth(username, password string) (string, bool) {
	sum := sha256.Sum256([]byte(password))
	var pwhash, role, version string
	if err := db.QueryRow(`SELECT pwhash,role,session_version FROM users WHERE username=?`, username).Scan(&pwhash, &role, &version); err != nil {
		bcrypt.CompareHashAndPassword(davDummyHash, []byte(password)) // constant-time wrt real path
		return "", false
	}
	if davCachedVersion(username, sum, version) {
		return role, true
	}
	if bcrypt.CompareHashAndPassword([]byte(pwhash), []byte(password)) != nil {
		return "", false
	}
	// A verifier racing a password reset can only cache the old version. The
	// next request checks the database version before using this cached entry.
	davCache.Store(username, davCacheEnt{role: role, pwSum: sum, version: version, exp: time.Now().Add(5 * time.Minute)})
	return role, true
}

// davReadMethods is an ALLOW-LIST of non-mutating methods. Anything not listed
// is treated as a write and refused for viewers (fail-closed). COPY writes its
// destination; LOCK/UNLOCK mutate lock state — all writes.
var davReadMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
	http.MethodPost:    true, // x/net/webdav treats POST like GET
	"PROPFIND":         true,
}

// davChallenge emits the 401 with WebDAV capability headers. Finder and the
// Windows mini-redirector send an UNAUTHENTICATED OPTIONS/PROPFIND probe first
// and abort if the 401 doesn't advertise WebDAV — so DAV and Allow must appear
// here too. Auth failures must be 401 (never 403) or clients won't prompt.
func davChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="filehub"`)
	w.Header().Set("DAV", "1, 2")
	w.Header().Set("Allow", "OPTIONS, GET, HEAD, POST, DELETE, PROPFIND, PROPPATCH, COPY, MOVE, LOCK, UNLOCK, PUT, MKCOL")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func davHandler() http.Handler {
	h := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: davFS{replacements: make(map[davReplacementPath]*davReplacement)},
		LockSystem: webdav.NewMemLS(), // class 2; nil makes Finder mount read-only
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("webdav %s %s: %v", r.Method, r.URL.Path, err)
			}
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, has := r.BasicAuth()
		if !has || user == "" {
			davChallenge(w)
			return
		}
		key := "dav:" + loginKey(r) // DAV failures cannot block browser/Android login.
		accountKey := accountLoginKey("dav", user)
		// Keep mounted clients with already verified credentials working while
		// rate limiting fresh password checks.
		role, ok := davAuthCached(user, pass)
		if !ok && passwordRetryAfter(key, accountKey) == "" {
			select {
			case passwordCheckSlots <- struct{}{}:
				role, ok = davAuth(user, pass)
				<-passwordCheckSlots
				if !ok || role == "" {
					recordPasswordFailure(key, accountKey)
				}
			default:
				w.Header().Set("Retry-After", "1")
			}
		}
		if !ok || role == "" {
			if retry := passwordRetryAfter(key, accountKey); retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			davChallenge(w)
			return
		}
		// viewer = read-only: refuse anything not on the read allow-list (incl.
		// LOCK/UNLOCK) up front. The FS-layer checks are the authoritative gate;
		// this is the transport-layer backup so a context wiring slip fails closed.
		account, err := loadProfile(user)
		if err != nil {
			davChallenge(w)
			return
		}
		role = account.role
		r = withProfile(r, account)
		if account.folders != nil {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		if role == "viewer" && !davReadMethods[r.Method] {
			http.Error(w, "read-only access", http.StatusForbidden)
			return
		}
		if account.folders != nil && !davReadMethods[r.Method] && !authorizeDAVWrite(r) {
			http.Error(w, "write access is limited to the personal folder", http.StatusForbidden)
			return
		}
		if r.Method == "GET" || r.Method == "HEAD" {
			if source, rel, full, ok := davResolve(strings.TrimPrefix(r.URL.Path, "/dav")); ok && full != "" &&
				account.folders.visibleEntry(source, rel) {
				originalHeaders(w, full, false)
			}
		}
		ctx := context.WithValue(r.Context(), davCtxKey{}, role)
		davTransfers(h).ServeHTTP(w, r.WithContext(ctx))
	})
}

func davCanWrite(ctx context.Context, source, rel string) bool {
	access := requestFolders(ctx)
	if access == nil {
		return true
	}
	account, err := loadProfile(ctx.Value(profileKey{}).(profileIdentity).username)
	if err != nil || account.role == "viewer" || account.folders == nil || !account.folders.canWrite(source, rel, false) {
		return false
	}
	if _, ok := realProfilePath(source, rel); ok {
		return true
	}
	// A missing leaf can be created only below an existing authorized parent.
	full, ok := safePath(source, rel)
	if !ok {
		return false
	}
	if _, err := os.Lstat(full); !os.IsNotExist(err) {
		return false
	}
	_, ok = realProfilePath(source, parentRel(rel))
	return ok
}

// COPY and MOVE may delete a destination first. Authorize both sides before
// handing the request to x/net/webdav, in addition to the filesystem checks.
func authorizeDAVWrite(r *http.Request) bool {
	source, rel, _, ok := davResolve(strings.TrimPrefix(r.URL.Path, "/dav"))
	if !ok {
		return false
	}
	switch r.Method {
	case "COPY", "MOVE":
		dest, err := url.Parse(r.Header.Get("Destination"))
		if err != nil || (dest.Host != "" && !strings.EqualFold(dest.Host, r.Host)) || !strings.HasPrefix(dest.Path, "/dav/") {
			return false
		}
		to, path, _, ok := davResolve(strings.TrimPrefix(dest.Path, "/dav"))
		if !ok || !davCanWrite(r.Context(), to, path) {
			return false
		}
		if r.Method == "MOVE" {
			return davCanWrite(r.Context(), source, rel)
		}
		_, readable := requestFolders(r.Context()).readPath(source, rel)
		return readable
	case "PUT", "DELETE", "MKCOL", "LOCK", "UNLOCK", "PROPPATCH":
		return davCanWrite(r.Context(), source, rel)
	default:
		return false
	}
}
