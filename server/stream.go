// Ephemeral signed playback links for streaming large media (movies) to an
// external player (VLC/MX) or the in-app player. External players don't send the
// session cookie, so /api/download (auth) is unreachable to them; instead the app
// asks for a short-lived HMAC-signed token and hands the resulting /play/<token>
// URL to the player. The file is served with http.ServeContent → Range requests,
// so the movie streams and seeks without being fully downloaded first.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type streamTicket struct {
	User, Version, Source, Path string
	Expires                     int64
	SessionHash                 string `json:",omitempty"`
}

func signStream(ticket streamTicket) string {
	data, _ := json.Marshal(ticket)
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, authSecret)
	mac.Write([]byte("stream:" + payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func verifyStream(token string) (streamTicket, bool) {
	var ticket streamTicket
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || len(payload) > 16384 {
		return ticket, false
	}
	mac := hmac.New(sha256.New, authSecret)
	mac.Write([]byte("stream:" + payload))
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature)) {
		return ticket, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &ticket) != nil || ticket.User == "" || time.Now().Unix() >= ticket.Expires {
		return streamTicket{}, false
	}
	return ticket, true
}

// handleStreamToken (authed): mint a 12h playback link for a specific file.
func handleStreamToken(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	full, ok := safePath(q.Get("source"), q.Get("path"))
	if !ok {
		http.Error(w, "bad source/path", http.StatusBadRequest)
		return
	}
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		http.Error(w, "not a file", http.StatusNotFound)
		return
	}
	account, authenticated := requestProfile(r)
	if !authenticated {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cookie, err := r.Cookie("fh_session")
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	parts := strings.SplitN(cookie.Value, "|", 3)
	if len(parts) != 3 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	until, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || until <= time.Now().Unix() {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tok := signStream(streamTicket{User: account.username, Version: account.version,
		Source: q.Get("source"), Path: q.Get("path"), Expires: min(until, time.Now().Add(12*time.Hour).Unix()),
		SessionHash: sha256hex(cookie.Value)})
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, map[string]any{"url": "/play/" + tok})
}

// handleStreamPlay (public): stream the file for the token, with Range support.
func handleStreamPlay(w http.ResponseWriter, r *http.Request) {
	ticket, ok := verifyStream(r.PathValue("token"))
	if !ok {
		http.Error(w, "invalid or expired link", http.StatusForbidden)
		return
	}
	account, err := loadProfile(ticket.User)
	if err != nil || !validRole(account.role) || account.version != ticket.Version {
		http.Error(w, "invalid or expired link", http.StatusForbidden)
		return
	}
	if ticket.SessionHash != "" {
		var revoked bool
		err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM revoked_sessions WHERE token_hash=?)`, ticket.SessionHash).Scan(&revoked)
		if err != nil || revoked {
			http.Error(w, "invalid or expired link", http.StatusForbidden)
			return
		}
	}
	full, ok := account.folders.readPath(ticket.Source, ticket.Path)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	originalHeaders(w, full, true)
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}
