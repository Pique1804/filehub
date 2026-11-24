// QR code generation for share links (scan a /s/<token> URL with a phone).
package main

import (
	"net/http"

	"github.com/skip2/go-qrcode"
)

func handleQR(w http.ResponseWriter, r *http.Request) {
	data := r.URL.Query().Get("data")
	if data == "" || len(data) > 2048 {
		http.Error(w, "bad data", http.StatusBadRequest)
		return
	}
	png, err := qrcode.Encode(data, qrcode.Medium, 320)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=3600")
	w.Write(png)
}
