// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	gerr "github.com/golusoris/golusoris/core/errors"
)

// Recover traps panics, writes an RFC 9457 internal-error response, and logs
// the panic value + stack at Error level with the request's X-Request-ID.
func Recover(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder, wrapped := newTrackingWriter(w)
			defer recoverRequest(logger, recorder, wrapped, r)
			next.ServeHTTP(wrapped, r)
		})
	}
}

func recoverRequest(logger *slog.Logger, recorder *trackingWriter, writer http.ResponseWriter, r *http.Request) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if recoveredErr, ok := recovered.(error); ok && errors.Is(recoveredErr, http.ErrAbortHandler) {
		panic(http.ErrAbortHandler)
	}
	logger.ErrorContext(
		r.Context(), "httpx: panic recovered",
		slog.Any("panic", recovered),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("request_id", RequestIDFromContext(r.Context())),
		slog.String("stack", string(debug.Stack())),
	)
	problem := gerr.ProblemFromError(
		gerr.Internal("panic detail must not escape"),
		http.StatusInternalServerError,
		r.URL.RequestURI(),
	)
	if recorder.committed() {
		panic(http.ErrAbortHandler)
	}
	if err := gerr.WriteProblem(writer, problem); err != nil {
		logger.ErrorContext(r.Context(), "httpx: encode recovery response",
			slog.String("error", err.Error()))
	}
}
