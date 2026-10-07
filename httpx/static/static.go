// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package static serves files from an [fs.FS] with ETag + cache-control
// headers tuned for typical web workloads.
//
// Typical use:
//
//	//go:embed assets
//	var assets embed.FS
//	r.Mount("/assets", static.Handler(assets, static.Options{}))
//
// Unhashed assets (index.html, robots.txt) get a short cache with ETag;
// callers using hashed filenames should prefer [httpx/static/hashfs] for
// immutable cache headers.
package static

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/golusoris/golusoris/core/validate"
)

// Options tunes the handler. Zero value uses sensible defaults.
type Options struct {
	// CacheControl is the value written as Cache-Control. Default
	// "public, max-age=300, must-revalidate".
	CacheControl string
	// NoIndexFallback disables the index.html fallback for "/" and
	// directory paths. Default false (fallback enabled).
	NoIndexFallback bool
}

// Handler returns an http.Handler that serves files from fsys. It reads and
// hashes the current file contents per request so mutable filesystems never
// serve a stale body or ETag. If-None-Match is honored with a 304 response.
func Handler(fsys fs.FS, opts Options) http.Handler {
	if validate.IsNil(fsys) {
		fsys = nil
	}
	if opts.CacheControl == "" {
		opts.CacheControl = "public, max-age=300, must-revalidate"
	}
	return &handler{fs: fsys, opts: opts}
}

type handler struct {
	fs   fs.FS
	opts Options
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.fs == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || strings.HasSuffix(path, "/") {
		if h.opts.NoIndexFallback {
			http.NotFound(w, r)
			return
		}
		path = strings.TrimSuffix(path, "/") + "/index.html"
		path = strings.TrimPrefix(path, "/")
	}

	b, err := fs.ReadFile(h.fs, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	etag := weakETag(b)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", h.opts.CacheControl)

	// Delegate content type, ranges, and RFC conditional semantics to stdlib;
	// ETag and Cache-Control are already present for its precondition checks.
	http.ServeContent(w, r, path, zeroTime, readSeeker(b))
}

// readSeeker adapts []byte into a io.ReadSeeker for http.ServeContent.
func readSeeker(b []byte) io.ReadSeeker { return bytes.NewReader(b) }

// zeroTime is passed to ServeContent because we don't track modtime; the
// ETag carries the freshness signal instead.
var zeroTime = time.Time{}

func weakETag(b []byte) string {
	sum := sha256.Sum256(b)
	return `W/"` + hex.EncodeToString(sum[:16]) + `"`
}
