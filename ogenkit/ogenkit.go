// Package ogenkit is the glue between ogen-generated code and golusoris
// conventions. It provides:
//
//   - [ErrorHandler] that translates golusoris [errors.Error] → proper HTTP
//     status + JSON error body, falling back to ogen's default for other
//     error types (including ogen's own DecodeRequestError, SecurityError,
//     etc.).
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
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"

	ogenmw "github.com/ogen-go/ogen/middleware"
	"github.com/ogen-go/ogen/ogenerrors"

	gerr "github.com/golusoris/golusoris/errors"
)

// errorBody is the JSON shape written by [ErrorHandler]. Matches the
// Problem-Details-lite convention used across the framework.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorHandler returns an ogenerrors.ErrorHandler that maps [*gerr.Error] to
// its code's HTTP status + JSON body. Non-golusoris errors delegate to
// ogen's DefaultErrorHandler, preserving ogen's own error types.
//
// Apps pass this via ogen's generated `WithErrorHandler` option.
func ErrorHandler(logger *slog.Logger) ogenerrors.ErrorHandler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
		var ge *gerr.Error
		if errors.As(err, &ge) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(ge.Status())
			if encErr := json.NewEncoder(w).Encode(errorBody{
				Code:    string(ge.Code),
				Message: ge.Message,
			}); encErr != nil {
				logger.ErrorContext(ctx, "ogenkit: encode error body",
					slog.String("error", encErr.Error()))
			}
			return
		}
		// Fall back to ogen's default (handles DecodeRequestError, etc.).
		ogenerrors.DefaultErrorHandler(ctx, w, r, err)
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
		logger.LogAttrs(req.Context, level, "ogen.operation",
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
				logger.ErrorContext(req.Context, "ogenkit: panic recovered",
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
