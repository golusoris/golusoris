// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package rangeserve provides HTTP range-request serving for large files
// (video playback, resumable downloads) backed by any io.ReadSeeker.
// Delegates to stdlib http.ServeContent for single and multipart ranges,
// Last-Modified, and conditional requests. ServeContent honors an ETag already
// set on the response; it does not generate one from modTime.
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
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
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
	return HandlerWithLogger(opener, keyFn, slog.Default())
}

// HandlerWithLogger is Handler with an explicit logger for storage failures.
func HandlerWithLogger(opener Opener, keyFn func(*http.Request) string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if validate.IsNil(opener) || keyFn == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.ErrorContext(r.Context(), "rangeserve: missing handler dependency")
			writeInternalProblem(w, r, logger)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		key := keyFn(r)
		rc, modTime, err := opener.Open(ctx, key)
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			logger.ErrorContext(r.Context(), "rangeserve: open", slog.String("key", key), slog.Any("err", err))
			writeInternalProblem(w, r, logger)
			return
		}
		if validate.IsNil(rc) {
			logger.ErrorContext(r.Context(), "rangeserve: opener returned nil content", slog.String("key", key))
			writeInternalProblem(w, r, logger)
			return
		}
		defer func(ctx context.Context) {
			if closeErr := rc.Close(); closeErr != nil {
				logger.WarnContext(ctx, "rangeserve: close", slog.String("key", key), slog.Any("err", closeErr))
			}
		}(ctx)
		http.ServeContent(w, r, key, modTime, rc)
	})
}

// ServeFile serves a single file from disk with full range-request support.
// This is a thin alias over http.ServeFile with a comment for discoverability.
func ServeFile(w http.ResponseWriter, r *http.Request, path string) {
	http.ServeFile(w, r, filepath.Clean(path))
}

// ServeReader serves content from an io.ReadSeeker with full range support.
// name is used for MIME sniffing; modTime controls Last-Modified. Set ETag on
// the response before calling ServeReader when validator handling is required.
func ServeReader(w http.ResponseWriter, r *http.Request, name string, modTime time.Time, content io.ReadSeeker) {
	if validate.IsNil(content) {
		writeInternalProblem(w, r, slog.Default())
		return
	}
	http.ServeContent(w, r, name, modTime, content)
}

func writeInternalProblem(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	problem := gerr.ProblemFromError(
		gerr.Internal("open content"),
		http.StatusInternalServerError,
		r.URL.RequestURI(),
	)
	if err := gerr.WriteProblem(w, problem); err != nil {
		logger.ErrorContext(r.Context(), "rangeserve: write problem", slog.Any("err", err))
	}
}
