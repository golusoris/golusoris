// Package rangeserve provides HTTP range-request serving for large files
// (video playback, resumable downloads) backed by any io.ReadSeeker.
// Delegates to stdlib http.ServeContent which implements RFC 7233 fully:
// single ranges, multi-part ranges, ETag, Last-Modified, If-Range.
//
// Usage:
//
//	mux.Handle("/videos/{id}", rangeserve.Handler(bucket))
//
//	// Or serve a single file:
//	rangeserve.ServeFile(w, r, "/var/media/movie.mp4")
package rangeserve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Opener is implemented by storage backends that can open objects for
// random-access reading. The returned ReadSeekCloser must support concurrent
// reads when the caller holds it open; it is closed when the HTTP handler
// returns.
type Opener interface {
	Open(ctx context.Context, key string) (io.ReadSeekCloser, time.Time, error)
}

// Handler returns an HTTP handler that serves the key extracted from the
// request by keyFn. Use with chi's URLParam or path.Base(r.URL.Path).
func Handler(opener Opener, keyFn func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := keyFn(r)
		rc, modTime, err := opener.Open(r.Context(), key)
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("rangeserve: open: %v", err), http.StatusInternalServerError)
			return
		}
		defer rc.Close() //nolint:errcheck
		http.ServeContent(w, r, key, modTime, rc)
	})
}

// ServeFile serves a single file from disk with full range-request support.
// This is a thin alias over http.ServeFile with a comment for discoverability.
func ServeFile(w http.ResponseWriter, r *http.Request, path string) {
	http.ServeFile(w, r, filepath.Clean(path))
}

// ServeReader serves content from an io.ReadSeeker with full range support.
// name is used for MIME sniffing; modTime controls Last-Modified + ETag.
func ServeReader(w http.ResponseWriter, r *http.Request, name string, modTime time.Time, content io.ReadSeeker) {
	http.ServeContent(w, r, name, modTime, content)
}
