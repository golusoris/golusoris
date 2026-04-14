package errors_test

import (
	stderrors "errors"
	"net/http"
	"testing"

	"github.com/golusoris/golusoris/errors"
)

func TestCodeStatus(t *testing.T) {
	t.Parallel()
	cases := map[errors.Code]int{
		errors.CodeBadRequest:   http.StatusBadRequest,
		errors.CodeUnauthorized: http.StatusUnauthorized,
		errors.CodeForbidden:    http.StatusForbidden,
		errors.CodeNotFound:     http.StatusNotFound,
		errors.CodeConflict:     http.StatusConflict,
		errors.CodeRateLimited:  http.StatusTooManyRequests,
		errors.CodeTimeout:      http.StatusGatewayTimeout,
		errors.CodeUnavailable:  http.StatusServiceUnavailable,
		errors.CodeInternal:     http.StatusInternalServerError,
		errors.CodeValidation:   http.StatusBadRequest,
		errors.CodeUnknown:      http.StatusInternalServerError,
	}
	for code, want := range cases {
		if got := code.Status(); got != want {
			t.Errorf("Code(%s).Status() = %d, want %d", code, got, want)
		}
	}
}

func TestWrapNilReturnsNil(t *testing.T) {
	t.Parallel()
	if errors.Wrap(nil, errors.CodeInternal, "x") != nil {
		t.Error("Wrap(nil, ...) should return nil")
	}
}

func TestUnwrap(t *testing.T) {
	t.Parallel()
	base := stderrors.New("base")
	wrapped := errors.Wrap(base, errors.CodeNotFound, "missing")
	if !stderrors.Is(wrapped, base) {
		t.Error("errors.Is should find wrapped cause")
	}
	if wrapped.Status() != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", wrapped.Status())
	}
}

func TestConstructors(t *testing.T) {
	t.Parallel()
	if e := errors.NotFound("nope"); e.Code != errors.CodeNotFound || e.Message != "nope" {
		t.Errorf("NotFound = %+v", e)
	}
	if e := errors.Validation("bad"); e.Status() != http.StatusBadRequest {
		t.Errorf("Validation status = %d", e.Status())
	}
}

func TestAllConstructors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  *errors.Error
		code errors.Code
		want int
	}{
		{"BadRequest", errors.BadRequest("bad"), errors.CodeBadRequest, http.StatusBadRequest},
		{"Unauthorized", errors.Unauthorized("unauth"), errors.CodeUnauthorized, http.StatusUnauthorized},
		{"Forbidden", errors.Forbidden("forb"), errors.CodeForbidden, http.StatusForbidden},
		{"Conflict", errors.Conflict("conf"), errors.CodeConflict, http.StatusConflict},
		{"Internal", errors.Internal("int"), errors.CodeInternal, http.StatusInternalServerError},
		{"RateLimited", errors.RateLimited("rl"), errors.CodeRateLimited, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.err.Code != tc.code {
				t.Errorf("Code = %v, want %v", tc.err.Code, tc.code)
			}
			if tc.err.Status() != tc.want {
				t.Errorf("Status = %d, want %d", tc.err.Status(), tc.want)
			}
		})
	}
}

func TestErrorString(t *testing.T) {
	t.Parallel()
	e := errors.New(errors.CodeNotFound, "thing not found")
	s := e.Error()
	if s == "" {
		t.Error("Error() returned empty string")
	}
}
