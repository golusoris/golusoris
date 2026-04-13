package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// etagRecorder buffers the response body so we can hash it after the handler
// finishes. Memory cost: one full response copy — acceptable for typical API
// payloads (KiB-MiB). Streaming responses should bypass this middleware.
type etagRecorder struct {
	http.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (e *etagRecorder) WriteHeader(code int) { e.status = code }
func (e *etagRecorder) Write(b []byte) (int, error) {
	return e.buf.Write(b) //nolint:wrapcheck // in-memory buffer, no meaningful error context to add
}

// ETag computes a weak-ETag (sha256 of body) for GET responses with 200 OK
// and honors If-None-Match to return 304. Non-GET and non-200 responses
// pass through unchanged.
func ETag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		rec := &etagRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write(rec.buf.Bytes())
			return
		}

		sum := sha256.Sum256(rec.buf.Bytes())
		etag := `W/"` + hex.EncodeToString(sum[:16]) + `"`
		w.Header().Set("ETag", etag)

		if match := r.Header.Get("If-None-Match"); match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(rec.buf.Bytes())
	})
}
