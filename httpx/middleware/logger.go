// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/validate"
)

// Logger emits one structured access log per request (at Info level for 2xx/
// 3xx, Warn for 4xx, Error for 5xx). Uses [clock.Clock] for time so tests
// can inject a fake.
func Logger(logger *slog.Logger, clk clock.Clock) Middleware {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			rec, wrapped := newTrackingWriter(w)
			completed := false
			defer func() {
				logRequest(logger, clk.Since(start), rec, r, !completed)
			}()
			next.ServeHTTP(wrapped, r)
			completed = true
		})
	}
}

func logRequest(logger *slog.Logger, elapsed time.Duration, rec *trackingWriter, r *http.Request, panicked bool) {
	if rec.flushErr != nil {
		logger.ErrorContext(r.Context(), "httpx: flush response",
			slog.String("error", rec.flushErr.Error()))
	}
	status := statusOrDefault(rec.status)
	if panicked && rec.status == 0 {
		status = http.StatusInternalServerError
	}
	level := slog.LevelInfo
	switch {
	case status >= http.StatusInternalServerError:
		level = slog.LevelError
	case status >= http.StatusBadRequest:
		level = slog.LevelWarn
	}
	logger.LogAttrs(
		r.Context(), level, "http",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Int("bytes", rec.bytes),
		slog.Duration("elapsed", elapsed),
		slog.String("remote", r.RemoteAddr),
		slog.String("request_id", RequestIDFromContext(r.Context())),
	)
}

func statusOrDefault(s int) int {
	if s == 0 {
		return http.StatusOK
	}
	return s
}
