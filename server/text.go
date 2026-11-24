package main

// A small single-writer editor. Events exist only while a document is open;
// documents remain ordinary UTF-8 files, with no background directory polling.
import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxTextBytes = 1 << 20
const textLease = 45 * time.Second

type textRoom struct {
	doc         *textDocument
	token, user string
	expires     time.Time
	timer       *time.Timer
	clients     map[chan []byte]bool
}

var textMu sync.Mutex
var textRooms = map[string]*textRoom{}

type textDocument struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
	Editing  bool   `json:"editing"`
	Token    string `json:"token,omitempty"`
}

func textRevision(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func textTarget(source, path string) (*os.Root, string, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".txt" && ext != ".md" {
		return nil, "", "", errors.New("редактор поддерживает только .txt и .md")
	}
	full, root, ok := resolveItem(source, path)
	if !ok || full == root {
		return nil, "", "", errors.New("недопустимый путь")
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return nil, "", "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		return nil, "", "", errors.New("папка не найдена")
	}
	r, err := os.OpenRoot(root)
	return r, rel, filepath.Join(parent, filepath.Base(full)), err
}

func readText(root *os.Root, rel string) ([]byte, os.FileMode, error) {
	// Never follow a final symlink or block on a FIFO/device disguised as .txt.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxTextBytes {
		return nil, 0, errors.New("нужен обычный текстовый файл не больше 1 МБ")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTextBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(b) > maxTextBytes || !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return nil, 0, errors.New("нужен текст UTF-8 не больше 1 МБ")
	}
	return b, st.Mode().Perm(), nil
}

func textActive(room *textRoom) bool {
	return room != nil && room.token != "" && time.Now().Before(room.expires)
}

// All room operations are serialized; buffers share immutable event bytes.
func textPublish(room *textRoom, value any) {
	if room == nil {
		return
	}
	// Coalesced events must always include the last text as well as lock state,
	// otherwise an immediate release could overwrite a queued save notification.
	switch v := value.(type) {
	case textDocument:
		v.Token = ""
		room.doc = &v
	case map[string]bool:
		if room.doc != nil {
			room.doc.Editing = v["editing"]
			value = *room.doc
		}
	}
	b, _ := json.Marshal(value)
	for ch := range room.clients {
		select {
		case ch <- b:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- b:
			default:
			}
		}
	}
}
func textPrune(key string, room *textRoom) {
	if !textActive(room) && len(room.clients) == 0 {
		if room.timer != nil {
			room.timer.Stop()
		}
		delete(textRooms, key)
	}
}
func textTouch(key string, room *textRoom) {
	room.expires = time.Now().Add(textLease)
	if room.timer != nil {
		room.timer.Stop()
	}
	room.timer = time.AfterFunc(textLease, func() {
		textMu.Lock()
		defer textMu.Unlock()
		if textRooms[key] != room || textActive(room) {
			return
		}
		room.token, room.user = "", ""
		textPublish(room, map[string]bool{"editing": false})
		textPrune(key, room)
	})
}
func textGetRoom(key string) (*textRoom, error) {
	if room := textRooms[key]; room != nil {
		return room, nil
	}
	if len(textRooms) >= 128 {
		return nil, errors.New("слишком много открытых документов")
	}
	room := &textRoom{clients: map[chan []byte]bool{}}
	textRooms[key] = room
	return room, nil
}

func handleText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	root, rel, key, err := textTarget(r.URL.Query().Get("source"), r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	defer root.Close()
	textMu.Lock()
	defer textMu.Unlock()
	b, _, err := readText(root, rel)
	if err != nil {
		http.Error(w, "не удалось открыть текстовый файл UTF-8 (до 1 МБ)", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, textDocument{Content: string(b), Revision: textRevision(b), Editing: textActive(textRooms[key])})
}

func handleTextEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !sameOrigin(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	root, rel, key, err := textTarget(r.URL.Query().Get("source"), r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	defer root.Close()
	textMu.Lock()
	b, _, err := readText(root, rel)
	if err != nil {
		textMu.Unlock()
		http.Error(w, "не удалось открыть текстовый файл", 400)
		return
	}
	room, err := textGetRoom(key)
	if err != nil || len(room.clients) >= 16 {
		textMu.Unlock()
		http.Error(w, "слишком много открытых документов", 429)
		return
	}
	ch := make(chan []byte, 1)
	room.clients[ch] = true
	room.doc = &textDocument{Content: string(b), Revision: textRevision(b), Editing: textActive(room)}
	initial, _ := json.Marshal(room.doc)
	textMu.Unlock()
	defer func() { textMu.Lock(); delete(room.clients, ch); textPrune(key, room); textMu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	allowed := func() bool {
		account, ok := authenticateProfile(r)
		if !ok {
			return false
		}
		_, allowed := account.folders.readPath(r.URL.Query().Get("source"), r.URL.Query().Get("path"))
		return allowed
	}
	send := func(data []byte) bool {
		if !allowed() {
			return false
		}
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !send(initial) {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			if !send(data) {
				return
			}
		case <-ticker.C:
			if !allowed() {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}

func writeTextAtomic(root *os.Root, rel string, b []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(rel), ".filehub-text-"+randomTextToken()+".tmp")
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	// Preserve ownership for files also accessed through Samba/WebDAV.
	st, statErr := root.Lstat(rel)
	if statErr != nil {
		f.Close()
		return statErr
	}
	if owner, ok := st.Sys().(*syscall.Stat_t); ok {
		if err = f.Chown(int(owner.Uid), int(owner.Gid)); err != nil {
			f.Close()
			return err
		}
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = root.Rename(tmp, rel); err != nil {
		return err
	}
	if dir, e := root.Open(filepath.Dir(rel)); e == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
func randomTextToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func handleTextEdit(w http.ResponseWriter, r *http.Request) {
	if requestFolders(r.Context()) != nil {
		fileMutationMu.Lock()
		defer fileMutationMu.Unlock()
		q := r.URL.Query()
		target := q.Get("path")
		if !profileWriteAllowed(w, r, q.Get("source"), parentRel(target), true) {
			return
		}
		if full, ok := safePath(q.Get("source"), target); !ok {
			http.NotFound(w, r)
			return
		} else if _, err := os.Lstat(full); !os.IsNotExist(err) &&
			!profileWriteAllowed(w, r, q.Get("source"), target, false) {
			return
		}
	}
	var req struct{ Action, Token, Revision, Content string }
	r.Body = http.MaxBytesReader(w, r.Body, 6*maxTextBytes+4096) // JSON escapes may expand UTF-8.
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "неверный запрос", 400)
		return
	}
	if len(req.Content) > maxTextBytes || !utf8.ValidString(req.Content) || strings.ContainsRune(req.Content, 0) {
		http.Error(w, "нужен текст UTF-8 не больше 1 МБ", 413)
		return
	}
	root, rel, key, err := textTarget(r.URL.Query().Get("source"), r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	defer root.Close()
	user, _, _ := currentUser(r)
	w.Header().Set("Cache-Control", "no-store")
	textMu.Lock()
	defer textMu.Unlock()
	if req.Action == "create" {
		f, e := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			http.Error(w, "файл уже существует или папка недоступна", 409)
			return
		}
		e = f.Sync()
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			http.Error(w, "ошибка сохранения", 500)
			return
		}
		indexAddPath(r.URL.Query().Get("source"), key, rel)
		writeJSON(w, textDocument{Revision: textRevision(nil)})
		return
	}
	if req.Action != "claim" && req.Action != "save" && req.Action != "renew" && req.Action != "release" {
		http.Error(w, "неизвестное действие", 400)
		return
	}
	room := textRooms[key]
	if req.Action == "release" || req.Action == "renew" {
		if !textActive(room) || room.token != req.Token || room.user != user {
			http.Error(w, "редактирование завершено; открой документ заново", 409)
			return
		}
		if req.Action == "renew" {
			textTouch(key, room)
		} else {
			room.token, room.user = "", ""
			textPublish(room, map[string]bool{"editing": false})
			textPrune(key, room)
			indexAddPath(r.URL.Query().Get("source"), key, rel)
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	b, mode, err := readText(root, rel)
	if err != nil {
		http.Error(w, "не удалось открыть текстовый файл UTF-8 (до 1 МБ)", 400)
		return
	}
	if req.Action == "claim" {
		if textActive(room) {
			http.Error(w, "документ уже редактируется на другом устройстве", 423)
			return
		}
		room, err = textGetRoom(key)
		if err != nil {
			http.Error(w, err.Error(), 429)
			return
		}
		room.token, room.user = randomTextToken(), user
		textTouch(key, room)
		textPublish(room, textDocument{Content: string(b), Revision: textRevision(b), Editing: true})
		writeJSON(w, textDocument{Content: string(b), Revision: textRevision(b), Editing: true, Token: room.token})
		return
	}
	if !textActive(room) || room.token != req.Token || room.user != user {
		http.Error(w, "право редактирования потеряно; сохрани черновик и открой документ заново", 409)
		return
	}
	if textRevision(b) != req.Revision {
		http.Error(w, "файл изменён вне редактора; сохрани черновик и открой документ заново", 409)
		return
	}
	if string(b) != req.Content {
		if err = writeTextAtomic(root, rel, []byte(req.Content), mode); err != nil {
			http.Error(w, "не удалось сохранить файл", 500)
			return
		}
	}
	textTouch(key, room)
	doc := textDocument{Content: req.Content, Revision: textRevision([]byte(req.Content)), Editing: true}
	textPublish(room, doc)
	writeJSON(w, doc)
}
