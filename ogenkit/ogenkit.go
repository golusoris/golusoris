// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ogenkit is the glue between ogen-generated code and golusoris
// conventions. It provides:
//
//   - [ErrorHandler] and [ErrorHandlerWith] that translate golusoris coded errors,
//     mapped sentinel errors ([WithStatus], [WithPublicStatus]), and validation failures ([WithInvalidParams])
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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	ogenmw "github.com/ogen-go/ogen/middleware"
	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"

	gerr "github.com/golusoris/golusoris/core/errors"
)

// Option configures an error handler built by [ErrorHandlerWith].
type Option func(*errorHandlerConfig)

type statusMapping struct {
	target error
	code   gerr.Code
	public bool
}

type errorHandlerConfig struct {
	statusMappings []statusMapping
	invalidParams  bool
}

// WithStatus maps an application sentinel error to a golusoris [gerr.Code]
// using [errors.Is]. First match wins, following the order in which options
// are supplied.
//
// Mapped 4xx responses use target.Error() as the detail message rather than
// the incoming error's Error() string to avoid leaking wrap context (such as
// internal queries or parameters) to clients. Mapped 5xx responses have their
// detail redacted to "internal server error".
func WithStatus(target error, code gerr.Code) Option {
	return func(cfg *errorHandlerConfig) {
		if target != nil {
			cfg.statusMappings = append(cfg.statusMappings, statusMapping{
				target: target,
				code:   code,
				public: false,
			})
		}
	}
}

// WithPublicStatus maps an application sentinel error to a golusoris [gerr.Code]
// using [errors.Is], exposing the incoming error's full error chain message as
// the problem detail even when the status is 5xx. First match wins, following
// the order in which options are supplied. A nil target is ignored.
//
// Detail exposure is deliberate; callers must only map errors whose text is safe
// for clients.
func WithPublicStatus(target error, code gerr.Code) Option {
	return func(cfg *errorHandlerConfig) {
		if target != nil {
			cfg.statusMappings = append(cfg.statusMappings, statusMapping{
				target: target,
				code:   code,
				public: true,
			})
		}
	}
}

// WithInvalidParams enables translation of ogen [validate.Error] occurrences
// anywhere in the error chain into HTTP 422 (Unprocessable Entity) problem
// details with CodeValidation ("validation"), detail "request validation failed",
// and the InvalidParams extension member populated from validation field errors.
func WithInvalidParams() Option {
	return func(cfg *errorHandlerConfig) {
		cfg.invalidParams = true
	}
}

// ErrorHandler returns an ogenerrors.ErrorHandler that emits RFC 9457 Problem
// Details while preserving ogen's status classification for its own errors.
//
// Apps pass this via ogen's generated `WithErrorHandler` option.
func ErrorHandler(logger *slog.Logger) ogenerrors.ErrorHandler {
	return ErrorHandlerWith(logger)
}

// ErrorHandlerWith returns an ogenerrors.ErrorHandler configured with custom
// error mapping options.
func ErrorHandlerWith(logger *slog.Logger, opts ...Option) ogenerrors.ErrorHandler {
	var cfg errorHandlerConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
		problem := resolveProblem(&cfg, r, err)
		if problem.Status >= http.StatusInternalServerError {
			logger.ErrorContext(ctx, "ogenkit: handler error",
				slog.String("error", err.Error()),
				slog.Int("status", problem.Status))
		}
		if encErr := writeProblem(w, problem); encErr != nil {
			logger.ErrorContext(ctx, "ogenkit: encode error body",
				slog.String("error", encErr.Error()))
		}
	}
}

// InvalidParam is one entry of the RFC 9457 "invalid-params" extension member.
type InvalidParam struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// problemBody adds the extension member here rather than in core/errors, so
// this module keeps building against the released core.
type problemBody struct {
	gerr.ProblemDetails

	InvalidParams []InvalidParam `json:"invalid-params,omitempty"` //nolint:tagliatelle // RFC 9457 extension member name is hyphenated
}

func writeProblem(w http.ResponseWriter, problem problemBody) error {
	w.Header().Set("Content-Type", gerr.ProblemJSONMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
	if err := json.NewEncoder(w).Encode(problem); err != nil {
		return fmt.Errorf("ogenkit: encode problem details: %w", err)
	}
	return nil
}

func resolveProblem(cfg *errorHandlerConfig, r *http.Request, err error) problemBody {
	instance := ""
	if r != nil && r.URL != nil {
		instance = r.URL.RequestURI()
	}

	if cfg.invalidParams {
		if valErr, ok := errors.AsType[*validate.Error](err); ok {
			problem := gerr.ProblemFromError(
				gerr.New(gerr.CodeValidation, "request validation failed"),
				http.StatusUnprocessableEntity,
				instance,
			)
			// CodeValidation maps to 400; RFC 9110 422 names a well-formed body that failed checks.
			problem.Status = http.StatusUnprocessableEntity
			problem.Title = http.StatusText(http.StatusUnprocessableEntity)
			return problemBody{ProblemDetails: problem, InvalidParams: validationParams(valErr)}
		}
	}

	for _, m := range cfg.statusMappings {
		if errors.Is(err, m.target) {
			problem := gerr.ProblemFromError(
				gerr.New(m.code, m.target.Error()),
				m.code.Status(),
				instance,
			)
			if m.public {
				detail := err.Error()
				problem.Detail = detail
				problem.ErrorMessage = detail
				problem.Message = detail
			}
			return problemBody{ProblemDetails: problem}
		}
	}

	return problemBody{ProblemDetails: gerr.ProblemFromError(err, ogenerrors.ErrorCode(err), instance)}
}

func validationParams(v *validate.Error) []InvalidParam {
	if v == nil || len(v.Fields) == 0 {
		return nil
	}
	out := make([]InvalidParam, 0, len(v.Fields))
	for _, f := range v.Fields {
		reason := ""
		if f.Error != nil {
			reason = f.Error.Error()
		}
		out = append(out, InvalidParam{Name: f.Name, Reason: reason})
	}
	return out
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
