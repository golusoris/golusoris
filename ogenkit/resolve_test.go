// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ogenkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/ogenkit"
)

func assertProblemEqual(t *testing.T, got, want ogenkit.Problem) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("Problem mismatch:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestResolverResolveWithNilRequest(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("item missing")
	opts := []ogenkit.Option{ogenkit.WithStatus(sentinel, gerr.CodeNotFound)}
	rv := ogenkit.NewResolver(opts...)

	prob := rv.Resolve(nil, sentinel)
	if prob.Instance != "" {
		t.Errorf("instance = %q, want empty", prob.Instance)
	}
	if prob.Status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", prob.Status, http.StatusNotFound)
	}

	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	rr := httptest.NewRecorder()
	ogenkit.ErrorHandlerWith(logger, opts...)(context.Background(), rr, nil, sentinel)

	var fromHandler ogenkit.Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &fromHandler); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertProblemEqual(t, prob, fromHandler)
}

func TestResolverResolveWithRequestMatchesHandler(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("item missing")
	opts := []ogenkit.Option{ogenkit.WithStatus(sentinel, gerr.CodeNotFound)}
	rv := ogenkit.NewResolver(opts...)

	req := httptest.NewRequest(http.MethodGet, "/items/123", nil)
	prob := rv.Resolve(req, sentinel)
	if prob.Instance != "/items/123" {
		t.Errorf("instance = %q, want /items/123", prob.Instance)
	}

	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	rr := httptest.NewRecorder()
	ogenkit.ErrorHandlerWith(logger, opts...)(context.Background(), rr, req, sentinel)

	var fromHandler ogenkit.Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &fromHandler); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertProblemEqual(t, prob, fromHandler)
}

func TestNewResolverNilOptions(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("item missing")
	rv := ogenkit.NewResolver(nil, ogenkit.WithStatus(sentinel, gerr.CodeNotFound), nil)
	prob := rv.Resolve(nil, sentinel)
	if prob.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", prob.Status)
	}
}

func TestWithFallbackDetailPlainError(t *testing.T) {
	t.Parallel()
	rv := ogenkit.NewResolver(ogenkit.WithFallbackDetail("service temporarily unavailable"))
	prob := rv.Resolve(nil, errors.New("db disconnect"))

	if prob.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", prob.Status)
	}
	if prob.Detail != "service temporarily unavailable" {
		t.Errorf("detail = %q, want public detail", prob.Detail)
	}
	if prob.ErrorMessage != "service temporarily unavailable" {
		t.Errorf("error_message = %q, want public detail", prob.ErrorMessage)
	}
	if prob.Message != "" {
		t.Errorf("message = %q, want empty for plain error", prob.Message)
	}
}

func TestWithFallbackDetailCodedInternal(t *testing.T) {
	t.Parallel()
	rv := ogenkit.NewResolver(ogenkit.WithFallbackDetail("service temporarily unavailable"))
	prob := rv.Resolve(nil, gerr.Internal("sensitive stack trace"))

	if prob.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", prob.Status)
	}
	if prob.Detail != "service temporarily unavailable" {
		t.Errorf("detail = %q, want public detail", prob.Detail)
	}
	if prob.ErrorMessage != "service temporarily unavailable" {
		t.Errorf("error_message = %q, want public detail", prob.ErrorMessage)
	}
	if prob.Message != "service temporarily unavailable" {
		t.Errorf("message = %q, want public message for coded error", prob.Message)
	}
}

func TestWithFallbackDetailDecodeUntouched(t *testing.T) {
	t.Parallel()
	rv := ogenkit.NewResolver(ogenkit.WithFallbackDetail("service temporarily unavailable"))
	decodeErr := &ogenerrors.DecodeRequestError{
		Name: "widget", ID: "Widget",
		Err: errors.New("invalid JSON payload"),
	}
	prob := rv.Resolve(nil, decodeErr)

	if prob.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", prob.Status)
	}
	if prob.Detail == "service temporarily unavailable" {
		t.Errorf("decode error detail was overwritten by fallback detail")
	}
}

func TestWithFallbackDetailEmptyPublicIgnored(t *testing.T) {
	t.Parallel()
	rv := ogenkit.NewResolver(ogenkit.WithFallbackDetail(""))
	prob := rv.Resolve(nil, errors.New("raw server fault"))

	if prob.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", prob.Status)
	}
	if prob.Detail != "internal server error" {
		t.Errorf("detail = %q, want 'internal server error'", prob.Detail)
	}
}

func TestWithValidationTypeSet(t *testing.T) {
	t.Parallel()
	valErr := &validate.Error{
		Fields: []validate.FieldError{
			{Name: "email", Error: errors.New("malformed format")},
		},
	}
	rv := ogenkit.NewResolver(ogenkit.WithInvalidParams(), ogenkit.WithValidationType("about:blank"))
	prob := rv.Resolve(nil, valErr)

	if prob.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", prob.Status)
	}
	if prob.Type != "about:blank" {
		t.Errorf("type = %q, want about:blank", prob.Type)
	}
	if len(prob.InvalidParams) != 1 || prob.InvalidParams[0].Name != "email" {
		t.Errorf("invalid-params = %+v, want field email", prob.InvalidParams)
	}
}

func TestWithValidationTypeDefaultURI(t *testing.T) {
	t.Parallel()
	valErr := &validate.Error{
		Fields: []validate.FieldError{
			{Name: "token", Error: errors.New("empty")},
		},
	}
	rv := ogenkit.NewResolver(ogenkit.WithInvalidParams())
	prob := rv.Resolve(nil, valErr)

	wantType := "https://golusoris.dev/errors/validation"
	if prob.Type != wantType {
		t.Errorf("type = %q, want %q", prob.Type, wantType)
	}
}

func TestWithValidationTypeWithoutInvalidParams(t *testing.T) {
	t.Parallel()
	valErr := &validate.Error{
		Fields: []validate.FieldError{
			{Name: "token", Error: errors.New("empty")},
		},
	}
	rv := ogenkit.NewResolver(ogenkit.WithValidationType("about:blank"))
	prob := rv.Resolve(nil, valErr)

	wantStatus := ogenerrors.ErrorCode(valErr)
	if prob.Status != wantStatus {
		t.Errorf("status = %d, want %d without WithInvalidParams", prob.Status, wantStatus)
	}
	if len(prob.InvalidParams) != 0 {
		t.Errorf("invalid-params should be empty, got %+v", prob.InvalidParams)
	}
}

func TestWithValidationTypeEmptyIgnored(t *testing.T) {
	t.Parallel()
	valErr := &validate.Error{
		Fields: []validate.FieldError{
			{Name: "token", Error: errors.New("empty")},
		},
	}
	rv := ogenkit.NewResolver(ogenkit.WithInvalidParams(), ogenkit.WithValidationType(""))
	prob := rv.Resolve(nil, valErr)

	wantType := "https://golusoris.dev/errors/validation"
	if prob.Type != wantType {
		t.Errorf("type = %q, want %q", prob.Type, wantType)
	}
}

func TestWithSecurityStatus(t *testing.T) {
	t.Parallel()
	secErr := fmt.Errorf("layer: %w", &ogenerrors.SecurityError{
		Security: "cookieAuth",
		Err:      errors.New("token expired"),
	})
	rv := ogenkit.NewResolver(ogenkit.WithSecurityStatus(http.StatusUnauthorized, "session expired"))
	prob := rv.Resolve(nil, secErr)

	if prob.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", prob.Status)
	}
	if prob.Type != "about:blank" {
		t.Errorf("type = %q, want about:blank", prob.Type)
	}
	if prob.Title != "Unauthorized" {
		t.Errorf("title = %q, want Unauthorized", prob.Title)
	}
	if prob.Detail != "session expired" {
		t.Errorf("detail = %q, want 'session expired'", prob.Detail)
	}
	if prob.ErrorMessage != "session expired" {
		t.Errorf("error_message = %q, want 'session expired'", prob.ErrorMessage)
	}

	raw, err := json.Marshal(prob)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "cookieAuth") {
		t.Errorf("body leaked security scheme: %s", string(raw))
	}
	if strings.Contains(string(raw), "token expired") {
		t.Errorf("body leaked token error: %s", string(raw))
	}
}

func TestWithSecurityStatusEmptyPublic(t *testing.T) {
	t.Parallel()
	secErr := fmt.Errorf("layer: %w", &ogenerrors.SecurityError{
		Security: "cookieAuth",
		Err:      errors.New("token expired"),
	})
	rv := ogenkit.NewResolver(ogenkit.WithSecurityStatus(http.StatusForbidden, ""))
	prob := rv.Resolve(nil, secErr)

	if prob.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", prob.Status)
	}
	if prob.Detail != "Forbidden" {
		t.Errorf("detail = %q, want 'Forbidden'", prob.Detail)
	}
	if prob.ErrorMessage != "Forbidden" {
		t.Errorf("error_message = %q, want 'Forbidden'", prob.ErrorMessage)
	}

	raw, err := json.Marshal(prob)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "cookieAuth") || strings.Contains(string(raw), "token expired") {
		t.Errorf("body leaked security details: %s", string(raw))
	}
}

func TestWithSecurityStatusInvalidBoundsIgnored(t *testing.T) {
	t.Parallel()
	secErr := fmt.Errorf("layer: %w", &ogenerrors.SecurityError{
		Security: "cookieAuth",
		Err:      errors.New("token expired"),
	})

	for _, invalidStatus := range []int{399, 600} {
		rv := ogenkit.NewResolver(ogenkit.WithSecurityStatus(invalidStatus, "should be ignored"))
		prob := rv.Resolve(nil, secErr)
		if prob.Status != http.StatusUnauthorized {
			t.Errorf("status for %d = %d, want 401 fallback", invalidStatus, prob.Status)
		}
		if prob.Detail == "should be ignored" {
			t.Errorf("detail for %d should not be set", invalidStatus)
		}
	}
}

func TestWithSecurityStatusValidBoundsAccepted(t *testing.T) {
	t.Parallel()
	secErr := &ogenerrors.SecurityError{Security: "bearer", Err: errors.New("denied")}

	rv400 := ogenkit.NewResolver(ogenkit.WithSecurityStatus(400, "bad auth"))
	prob400 := rv400.Resolve(nil, secErr)
	if prob400.Status != 400 || prob400.Detail != "bad auth" {
		t.Errorf("status 400: got status %d, detail %q", prob400.Status, prob400.Detail)
	}

	rv599 := ogenkit.NewResolver(ogenkit.WithSecurityStatus(599, "edge server auth"))
	prob599 := rv599.Resolve(nil, secErr)
	if prob599.Status != 599 || prob599.Detail != "edge server auth" {
		t.Errorf("status 599: got status %d, detail %q", prob599.Status, prob599.Detail)
	}
}

func TestWithSecurityStatusWithoutOptionFallback(t *testing.T) {
	t.Parallel()
	secErr := &ogenerrors.SecurityError{Security: "bearer", Err: errors.New("bad token")}
	rv := ogenkit.NewResolver()
	prob := rv.Resolve(nil, secErr)

	if prob.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", prob.Status)
	}
	if prob.Title != "Unauthorized" {
		t.Errorf("title = %q, want Unauthorized", prob.Title)
	}
}

func TestWithSecurityStatusLastCallWins(t *testing.T) {
	t.Parallel()
	secErr := &ogenerrors.SecurityError{Security: "bearer", Err: errors.New("bad token")}
	rv := ogenkit.NewResolver(
		ogenkit.WithSecurityStatus(http.StatusUnauthorized, "first"),
		ogenkit.WithSecurityStatus(http.StatusForbidden, "second"),
	)
	prob := rv.Resolve(nil, secErr)

	if prob.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", prob.Status)
	}
	if prob.Detail != "second" {
		t.Errorf("detail = %q, want 'second'", prob.Detail)
	}
}
