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
