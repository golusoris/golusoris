// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ogenkit is the glue between ogen-generated code and golusoris
// conventions. It provides:
//
//   - [ErrorHandler] and [ErrorHandlerWith] that translate golusoris coded errors,
//     mapped sentinel errors ([WithStatus], [WithPublicStatus], [WithHTTPStatus]), and validation failures ([WithInvalidParams])
//     into RFC 9457 Problem Details with their proper HTTP status.
//   - [Resolver] for resolving errors in ogen Handler.NewError implementations
//     into RFC 9457 [Problem] responses.
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

// Option configures an error handler built by [ErrorHandlerWith] or a [Resolver]
// built by [NewResolver].
type Option func(*errorHandlerConfig)

type statusMapping struct {
	target error
	code   gerr.Code
	public bool
	// httpStatus non-zero selects a [WithHTTPStatus] mapping; code is unused then.
	httpStatus int
	detail     string
}

type errorHandlerConfig struct {
	statusMappings []statusMapping
	invalidParams  bool
	pathInstance   bool
	decodeDetail   string
	fallbackDetail string
	validationType string
	securityStatus int
	securityDetail string
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

// WithHTTPStatus maps an application sentinel error to any HTTP status in
// 400..599 using [errors.Is], for statuses no [gerr.Code] carries (such as 502
// or 422). The problem detail is public; an empty public exposes the incoming
// error's full chain message, a deliberate caller opt-in like
// [WithPublicStatus]. First match wins across all mapping options, following
// the order in which options are supplied. A nil target or a status outside
// 400..599 is ignored.
func WithHTTPStatus(target error, status int, public string) Option {
	return func(cfg *errorHandlerConfig) {
		if target == nil || status < http.StatusBadRequest || status > maxProblemStatus {
			return
		}
		cfg.statusMappings = append(cfg.statusMappings, statusMapping{
			target:     target,
			httpStatus: status,
			detail:     public,
		})
	}
}

// WithPathInstance sets the problem instance to the request URL path only,
// so query strings never reach the response body.
func WithPathInstance() Option {
	return func(cfg *errorHandlerConfig) {
		cfg.pathInstance = true
	}
}

// WithDecodeDetail replaces the problem detail of ogen request and parameter
// decode errors with the fixed public text, instead of ogen's raw decoder
// message. Status classification stays ogen's. An empty public is ignored.
func WithDecodeDetail(public string) Option {
	return func(cfg *errorHandlerConfig) {
		if public != "" {
			cfg.decodeDetail = public
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

// WithFallbackDetail sets the problem detail and error message for unmapped
// errors whose status is 500 or higher. If problem.Message is populated, it is
// also updated to the public detail. An empty public string is ignored.
func WithFallbackDetail(public string) Option {
	return func(cfg *errorHandlerConfig) {
		if public != "" {
			cfg.fallbackDetail = public
		}
	}
}

// WithValidationType sets the RFC 9457 problem type URI for validation failures.
// It only takes effect when [WithInvalidParams] is enabled. An empty uri is ignored.
func WithValidationType(uri string) Option {
	return func(cfg *errorHandlerConfig) {
		if uri != "" {
			cfg.validationType = uri
		}
	}
}

// WithSecurityStatus maps security errors to the given HTTP status and public detail.
// An empty public detail uses the standard HTTP status text. A status outside 400..599 is ignored.
func WithSecurityStatus(status int, public string) Option {
	return func(cfg *errorHandlerConfig) {
		if status < http.StatusBadRequest || status > maxProblemStatus {
			return
		}
		cfg.securityStatus = status
		cfg.securityDetail = public
	}
}

// Resolver translates application errors into RFC 9457 [Problem] responses for
// ogen Handler.NewError(ctx, err), which has no request, sharing its mapping table with [ErrorHandlerWith].
type Resolver struct {
	cfg errorHandlerConfig
}

// NewResolver constructs a [Resolver] configured with the supplied options.
// Non-nil options are applied once in the order provided.
func NewResolver(opts ...Option) *Resolver {
	var cfg errorHandlerConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &Resolver{cfg: cfg}
}

// Resolve translates err into a [Problem]. The HTTP request r may be nil, in
// which case the instance member remains empty.
func (rv *Resolver) Resolve(r *http.Request, err error) Problem {
	return resolveProblem(&rv.cfg, r, err)
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
	rv := NewResolver(opts...)
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
		problem := rv.Resolve(r, err)
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

// Problem is the RFC 9457 body that [ErrorHandlerWith] writes and
// [Resolver.Resolve] returns.
type Problem struct {
	gerr.ProblemDetails

	InvalidParams []InvalidParam `json:"invalid-params,omitempty"` //nolint:tagliatelle // RFC 9457 extension member name is hyphenated
}

func writeProblem(w http.ResponseWriter, problem Problem) error {
	w.Header().Set("Content-Type", gerr.ProblemJSONMediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
	if err := json.NewEncoder(w).Encode(problem); err != nil {
		return fmt.Errorf("ogenkit: encode problem details: %w", err)
	}
	return nil
}

// maxProblemStatus is the highest status RFC 9110 classifies as an error.
const maxProblemStatus = 599

func problemInstance(cfg *errorHandlerConfig, r *http.Request) string {
	switch {
	case r == nil || r.URL == nil:
		return ""
	case cfg.pathInstance:
		return r.URL.Path
	default:
		return r.URL.RequestURI()
	}
}

func isDecodeError(err error) bool {
	if _, ok := errors.AsType[*ogenerrors.DecodeRequestError](err); ok {
		return true
	}
	_, ok := errors.AsType[*ogenerrors.DecodeParamsError](err)
	return ok
}

func httpStatusProblem(m statusMapping, err error, instance string) Problem {
	detail := m.detail
	if detail == "" {
		detail = err.Error()
	}
	return Problem{
		ProblemDetails: gerr.ProblemDetails{
			Type:         "about:blank",
			Title:        http.StatusText(m.httpStatus),
			Status:       m.httpStatus,
			Detail:       detail,
			Instance:     instance,
			ErrorMessage: detail,
		},
	}
}

func codedProblem(m statusMapping, err error, instance string) Problem {
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
	return Problem{ProblemDetails: problem}
}

func securityProblem(cfg *errorHandlerConfig, instance string) Problem {
	status := cfg.securityStatus
	detail := cfg.securityDetail
	if detail == "" {
		detail = http.StatusText(status)
	}
	return Problem{
		ProblemDetails: gerr.ProblemDetails{
			Type:         "about:blank",
			Title:        http.StatusText(status),
			Status:       status,
			Detail:       detail,
			Instance:     instance,
			ErrorMessage: detail,
		},
	}
}

func validationProblem(cfg *errorHandlerConfig, valErr *validate.Error, instance string) Problem {
	problem := gerr.ProblemFromError(
		gerr.New(gerr.CodeValidation, "request validation failed"),
		http.StatusUnprocessableEntity,
		instance,
	)
	// CodeValidation maps to 400; RFC 9110 422 names a well-formed body that failed checks.
	problem.Status = http.StatusUnprocessableEntity
	problem.Title = http.StatusText(http.StatusUnprocessableEntity)
	if cfg.validationType != "" {
		problem.Type = cfg.validationType
	}
	return Problem{ProblemDetails: problem, InvalidParams: validationParams(valErr)}
}

func fallbackProblem(cfg *errorHandlerConfig, err error, instance string) Problem {
	problem := gerr.ProblemFromError(err, ogenerrors.ErrorCode(err), instance)
	if cfg.decodeDetail != "" && problem.Status < http.StatusInternalServerError && isDecodeError(err) {
		problem.Detail = cfg.decodeDetail
		problem.ErrorMessage = cfg.decodeDetail
	}
	if cfg.fallbackDetail != "" && problem.Status >= http.StatusInternalServerError {
		problem.Detail = cfg.fallbackDetail
		problem.ErrorMessage = cfg.fallbackDetail
		if problem.Message != "" {
			problem.Message = cfg.fallbackDetail
		}
	}
	return Problem{ProblemDetails: problem}
}

func resolveProblem(cfg *errorHandlerConfig, r *http.Request, err error) Problem {
	instance := problemInstance(cfg, r)

	if cfg.invalidParams {
		if valErr, ok := errors.AsType[*validate.Error](err); ok {
			return validationProblem(cfg, valErr, instance)
		}
	}

	if cfg.securityStatus != 0 {
		if _, ok := errors.AsType[*ogenerrors.SecurityError](err); ok {
			return securityProblem(cfg, instance)
		}
	}

	for _, m := range cfg.statusMappings {
		if !errors.Is(err, m.target) {
			continue
		}
		if m.httpStatus != 0 {
			return httpStatusProblem(m, err, instance)
		}
		return codedProblem(m, err, instance)
	}

	return fallbackProblem(cfg, err, instance)
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
