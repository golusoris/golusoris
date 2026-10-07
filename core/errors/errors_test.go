// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package errors_test

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/errors"
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

func TestProblemFromError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		err          error
		fallback     int
		wantStatus   int
		wantType     string
		wantDetail   string
		wantCode     string
		wantErrField string
	}{
		{
			name:         "coded client error",
			err:          errors.NotFound("user not found"),
			fallback:     http.StatusInternalServerError,
			wantStatus:   http.StatusNotFound,
			wantType:     "https://golusoris.dev/errors/not_found",
			wantDetail:   "user not found",
			wantCode:     "not_found",
			wantErrField: "user not found",
		},
		{
			name:         "coded server error is sanitized",
			err:          errors.New(errors.CodeUnavailable, "postgres password leaked"),
			fallback:     http.StatusBadRequest,
			wantStatus:   http.StatusServiceUnavailable,
			wantType:     "https://golusoris.dev/errors/unavailable",
			wantDetail:   "internal server error",
			wantCode:     "unavailable",
			wantErrField: "internal server error",
		},
		{
			name:         "uncoded client error",
			err:          stderrors.New("invalid JSON"),
			fallback:     http.StatusBadRequest,
			wantStatus:   http.StatusBadRequest,
			wantType:     "about:blank",
			wantDetail:   "invalid JSON",
			wantErrField: "invalid JSON",
		},
		{
			name:         "invalid fallback is safe",
			err:          stderrors.New("secret"),
			fallback:     0,
			wantStatus:   http.StatusInternalServerError,
			wantType:     "about:blank",
			wantDetail:   "internal server error",
			wantErrField: "internal server error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problem := errors.ProblemFromError(tc.err, tc.fallback, "/items/42")
			if problem.Status != tc.wantStatus {
				t.Errorf("Status = %d, want %d", problem.Status, tc.wantStatus)
			}
			if problem.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", problem.Type, tc.wantType)
			}
			if problem.Detail != tc.wantDetail {
				t.Errorf("Detail = %q, want %q", problem.Detail, tc.wantDetail)
			}
			if problem.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", problem.Code, tc.wantCode)
			}
			if problem.ErrorMessage != tc.wantErrField {
				t.Errorf("ErrorMessage = %q, want %q", problem.ErrorMessage, tc.wantErrField)
			}
			if problem.Instance != "/items/42" {
				t.Errorf("Instance = %q", problem.Instance)
			}
			if strings.Contains(problem.Detail, "leaked") {
				t.Errorf("Detail leaked secret: %q", problem.Detail)
			}
		})
	}
}

func TestWriteProblemPreservesLegacyExtensions(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	problem := errors.ProblemFromError(errors.NotFound("missing"), http.StatusInternalServerError, "/items/1")
	if err := errors.WriteProblem(response, problem); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"detail", "message", "error_message"} {
		if body[field] != "missing" {
			t.Errorf("%s = %#v, want missing", field, body[field])
		}
	}
}

func TestWriteProblem(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	problem := errors.ProblemFromError(errors.NotFound("user"), http.StatusInternalServerError, "/users/42")
	if err := errors.WriteProblem(rr, problem); err != nil {
		t.Fatalf("WriteProblem: %v", err)
	}
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if got := rr.Header().Get("Content-Type"); got != errors.ProblemJSONMediaType {
		t.Errorf("Content-Type = %q", got)
	}
	var got errors.ProblemDetails
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got != problem {
		t.Errorf("body = %#v, want %#v", got, problem)
	}
}
