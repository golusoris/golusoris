// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ogenkit is the glue between ogen-generated code and golusoris
// conventions. It provides:
//
//   - [ErrorHandler] that translates golusoris coded errors and ogen errors
//     into RFC 9457 Problem Details with their proper HTTP status.
//   - [SlogMiddleware] / [RecoverMiddleware] — ogen middleware implementations
//     that integrate with the framework's slog logger. Use alongside the
//     httpx/middleware stack on the outer chi router; these cover spans
//     that run after ogen parameter/body decoding.
//
// ogen's generated Server type takes an Option chain — typically:
//
//	srv, err := api.NewServer(handler,
//	    api.WithErrorHandler(ogenkit.ErrorHandler(logger)),
//	    api.WithMiddleware(
//	        ogenkit.SlogMiddleware(logger),
//	        ogenkit.RecoverMiddleware(logger),
//	    ),
//	)
package ogenkit

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"

	ogenmw "github.com/ogen-go/ogen/middleware"
	"github.com/ogen-go/ogen/ogenerrors"

	gerr "github.com/golusoris/golusoris/core/errors"
)

// ErrorHandler returns an ogenerrors.ErrorHandler that emits RFC 9457 Problem
// Details while preserving ogen's status classification for its own errors.
//
// Apps pass this via ogen's generated `WithErrorHandler` option.
func ErrorHandler(logger *slog.Logger) ogenerrors.ErrorHandler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
		problem := gerr.ProblemFromError(err, ogenerrors.ErrorCode(err), r.URL.RequestURI())
		if problem.Status >= http.StatusInternalServerError {
			logger.ErrorContext(ctx, "ogenkit: handler error",
				slog.String("error", err.Error()),
				slog.Int("status", problem.Status))
		}
		if encErr := gerr.WriteProblem(w, problem); encErr != nil {
			logger.ErrorContext(ctx, "ogenkit: encode error body",
				slog.String("error", encErr.Error()))
		}
	}
}

// SlogMiddleware emits a structured log per ogen operation. Complements
// httpx/middleware.Logger — the outer middleware logs the HTTP request;
// this one adds operation-level attributes (operation ID, typed params).
func SlogMiddleware(logger *slog.Logger) ogenmw.Middleware {
	return func(req ogenmw.Request, next ogenmw.Next) (ogenmw.Response, error) {
		resp, err := next(req)
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		logger.LogAttrs(
			req.Context, level, "ogen.operation",
			slog.String("operation_id", req.OperationID),
			slog.String("operation_name", req.OperationName),
		)
		return resp, err
	}
}

// RecoverMiddleware traps panics inside ogen handlers and converts them into
// a 500 response via the framework's error path. Place after SlogMiddleware
// so the panic is logged with operation context.
func RecoverMiddleware(logger *slog.Logger) ogenmw.Middleware {
	return func(req ogenmw.Request, next ogenmw.Next) (resp ogenmw.Response, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.ErrorContext(
					req.Context, "ogenkit: panic recovered",
					slog.Any("panic", rec),
					slog.String("operation_id", req.OperationID),
					slog.String("stack", string(debug.Stack())),
				)
				err = gerr.Internal("internal server error")
			}
		}()
		return next(req)
	}
}
