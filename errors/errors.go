// Package errors is golusoris's error package: thin layer over [go-faster/errors]
// adding a typed Code + HTTP status mapping that ogenkit and HTTP middleware
// understand.
//
// All app code should construct domain errors via [New], [Wrap], or one of the
// convenience constructors ([NotFound], [BadRequest], etc.). Callers can
// [errors.Is] or [errors.As] them as usual.
package errors

import (
	"net/http"

	xerr "github.com/go-faster/errors"
)

// Code is a stable, machine-readable error class. It maps to an HTTP status
// via [Code.Status] and is exposed in API responses.
type Code string

// Standard codes. Add app-specific ones in app code; do not extend this list.
const (
	CodeUnknown      Code = "unknown"
	CodeBadRequest   Code = "bad_request"
	CodeUnauthorized Code = "unauthorized"
	CodeForbidden    Code = "forbidden"
	CodeNotFound     Code = "not_found"
	CodeConflict     Code = "conflict"
	CodeUnavailable  Code = "unavailable"
	CodeTimeout      Code = "timeout"
	CodeInternal     Code = "internal"
	CodeValidation   Code = "validation"
	CodeRateLimited  Code = "rate_limited"
)

// Status maps a [Code] to an HTTP status code. ogenkit uses this to build
// responses; middleware uses it for access logs.
func (c Code) Status() int {
	switch c {
	case CodeBadRequest, CodeValidation:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeTimeout:
		return http.StatusGatewayTimeout
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeInternal, CodeUnknown:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// Error is the canonical golusoris error type.
type Error struct {
	Code    Code
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return e.Message + ": " + e.Cause.Error()
}

// Unwrap supports [errors.Is] / [errors.As].
func (e *Error) Unwrap() error { return e.Cause }

// Status returns the HTTP status code for this error.
func (e *Error) Status() int { return e.Code.Status() }

// New builds a coded error.
func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// Wrap attaches a code + message to an existing error. nil-in / nil-out.
func Wrap(err error, code Code, msg string) *Error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Message: msg, Cause: err}
}

// Convenience constructors — each builds a coded *Error from a single message.

// NotFound is sugar for [New]([CodeNotFound], msg).
func NotFound(msg string) *Error { return New(CodeNotFound, msg) }

// BadRequest is sugar for [New]([CodeBadRequest], msg).
func BadRequest(msg string) *Error { return New(CodeBadRequest, msg) }

// Unauthorized is sugar for [New]([CodeUnauthorized], msg).
func Unauthorized(msg string) *Error { return New(CodeUnauthorized, msg) }

// Forbidden is sugar for [New]([CodeForbidden], msg).
func Forbidden(msg string) *Error { return New(CodeForbidden, msg) }

// Conflict is sugar for [New]([CodeConflict], msg).
func Conflict(msg string) *Error { return New(CodeConflict, msg) }

// Validation is sugar for [New]([CodeValidation], msg).
func Validation(msg string) *Error { return New(CodeValidation, msg) }

// Internal is sugar for [New]([CodeInternal], msg).
func Internal(msg string) *Error { return New(CodeInternal, msg) }

// RateLimited is sugar for [New]([CodeRateLimited], msg).
func RateLimited(msg string) *Error { return New(CodeRateLimited, msg) }

// Re-exports of go-faster/errors helpers so callers don't need two error pkgs.
var (
	Is     = xerr.Is
	As     = xerr.As
	Unwrap = xerr.Unwrap
	Errorf = xerr.Errorf
)
