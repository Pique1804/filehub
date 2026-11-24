package main

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func davPutQuota(ctx context.Context, full string, length int64) (*quotaReservation, *int64, error) {
	account, err := quotaAccount(ctx)
	if err != nil || account.personalLimit() == 0 {
		return nil, nil, err
	}
	var existing int64
	if info, err := os.Lstat(full); err == nil && info.Mode().IsRegular() {
		existing = info.Size()
	}
	return reservePersonalWrite(ctx, account, length, existing, math.MaxInt64)
}

// COPY may remove an existing destination before opening the first file. Check
// ordinary quota failures before x/net/webdav can mutate the destination.
func checkDAVCopyQuota(r *http.Request) error {
	account, err := quotaAccount(r.Context())
	if err != nil || account.personalLimit() == 0 {
		return err
	}
	dest, err := url.Parse(r.Header.Get("Destination"))
	if err != nil {
		return nil
	} // The WebDAV handler validates the destination.
	_, _, from, fromOK := davResolve(strings.TrimPrefix(r.URL.Path, "/dav"))
	_, _, to, toOK := davResolve(strings.TrimPrefix(dest.Path, "/dav"))
	if !fromOK || !toOK || from == "" || to == "" {
		return nil
	}
	info, err := os.Lstat(from)
	if err != nil {
		return nil
	}
	if info.IsDir() && r.Header.Get("Depth") == "0" {
		return nil
	}
	size, err := quotaPathBytes(r.Context(), from)
	if err != nil {
		return errQuotaUnavailable
	}
	replaced, err := quotaPathBytes(r.Context(), to)
	if err != nil && !os.IsNotExist(err) {
		return errQuotaUnavailable
	}
	return checkQuotaGrowth(r.Context(), account, size-replaced, "")
}

// The WebDAV library maps filesystem errors to generic protocol responses.
// Keep the HTTP status and message that specifically describe quota failures.
type davQuotaResponse struct {
	http.ResponseWriter
	transfer *davTransfer
	replaced bool
}

func (w *davQuotaResponse) WriteHeader(status int) {
	if status >= 400 && writeQuotaError(w.ResponseWriter, w.transfer.quotaErr) {
		w.replaced = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *davQuotaResponse) Write(data []byte) (int, error) {
	if w.replaced {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}
