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

	ogenmw "github.com/ogen-go/ogen/middleware"
	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/ogenkit"
)

func TestErrorHandlerMapsGolusorisError(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandler(logger)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/42?view=full", nil)
	h(context.Background(), rr, req, gerr.NotFound("user"))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]any{
		"type":     "https://golusoris.dev/errors/not_found",
		"title":    "Not Found",
		"status":   float64(http.StatusNotFound),
		"detail":   "user",
		"instance": "/users/42?view=full",
		"code":     "not_found",
		"message":  "user",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %#v, want %#v", key, body[key], value)
		}
	}
}

func TestErrorHandlerFormatsOgenErrorsAsProblemDetails(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandler(logger)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/widgets", nil)
	decodeErr := &ogenerrors.DecodeRequestError{
		Name: "createWidget", ID: "CreateWidget",
		Err: errors.New("invalid JSON"),
	}
	h(context.Background(), rr, req, decodeErr)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]any{
		"type":          "about:blank",
		"title":         "Bad Request",
		"status":        float64(http.StatusBadRequest),
		"detail":        decodeErr.Error(),
		"instance":      "/widgets",
		"error_message": decodeErr.Error(),
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %#v, want %#v", key, body[key], value)
		}
	}
}

func TestErrorHandlerHidesInternalErrorDetail(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	h := ogenkit.ErrorHandler(logger)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
	h(context.Background(), rr, req, errors.New("database password leaked"))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "password") {
		t.Errorf("body leaked internal error: %s", rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["detail"] != "internal server error" {
		t.Errorf("detail = %#v", body["detail"])
	}
	if !strings.Contains(logBuf.String(), "database password leaked") {
		t.Errorf("log missing internal error: %s", logBuf.String())
	}
}

func assertServerErrorRedacted(t *testing.T, err error, wantStatus int) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandler(logger)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/widgets", nil)

	h(context.Background(), rr, req, err)

	if rr.Code != wantStatus {
		t.Errorf("status = %d, want %d", rr.Code, wantStatus)
	}
	if strings.Contains(rr.Body.String(), "leaked") {
		t.Errorf("body leaked server error: %s", rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, field := range []string{"detail", "message"} {
		if body[field] != "internal server error" {
			t.Errorf("%s = %#v", field, body[field])
		}
	}
}

func TestErrorHandlerHidesCodedServerErrorDetail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "internal", err: gerr.Internal("database password leaked"), want: http.StatusInternalServerError},
		{name: "unavailable", err: gerr.New(gerr.CodeUnavailable, "upstream token leaked"), want: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertServerErrorRedacted(t, tc.err, tc.want)
		})
	}
}

func TestRecoverMiddlewareConvertsPanicToError(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	mw := ogenkit.RecoverMiddleware(logger)

	_, err := mw(ogenmw.Request{Context: context.Background()}, func(_ ogenmw.Request) (ogenmw.Response, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error from recovered panic")
	}
	var ge *gerr.Error
	if !errors.As(err, &ge) {
		t.Fatalf("expected *gerr.Error, got %T", err)
	}
	if ge.Code != gerr.CodeInternal {
		t.Errorf("Code = %s, want internal", ge.Code)
	}
}

func TestSlogMiddlewareLogsOperation(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	mw := ogenkit.SlogMiddleware(logger)

	_, err := mw(
		ogenmw.Request{Context: context.Background(), OperationID: "ListUsers", OperationName: "List users"},
		func(_ ogenmw.Request) (ogenmw.Response, error) { return ogenmw.Response{}, nil },
	)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"operation_id":"ListUsers"`, `"msg":"ogen.operation"`} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("log missing %q: %q", want, out)
		}
	}
}

func TestErrorHandlerWithMappedSentinelWrappedTwice(t *testing.T) {
	t.Parallel()
	errSentinel := errors.New("user not found")
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithStatus(errSentinel, gerr.CodeNotFound))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	wrapped := fmt.Errorf("service layer: %w", fmt.Errorf("repo layer: %w", errSentinel))
	h(context.Background(), rr, req, wrapped)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code = %v, want not_found", body["code"])
	}
	if body["status"] != float64(http.StatusNotFound) {
		t.Errorf("status = %v, want 404", body["status"])
	}
	if body["type"] != "https://golusoris.dev/errors/not_found" {
		t.Errorf("type = %v, want https://golusoris.dev/errors/not_found", body["type"])
	}
	if body["detail"] != "user not found" {
		t.Errorf("detail = %v, want 'user not found' (unleaked target message)", body["detail"])
	}
}

func assertInvalidParam(t *testing.T, item any, wantName, wantReason string) {
	t.Helper()
	p, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("item not a map: %#v", item)
	}
	if p["name"] != wantName || p["reason"] != wantReason {
		t.Errorf("param = %#v, want name=%s, reason=%s", p, wantName, wantReason)
	}
}

func TestErrorHandlerWithInvalidParams(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithInvalidParams())

	valErr := &validate.Error{
		Fields: []validate.FieldError{
			{Name: "page_size", Error: errors.New("must be positive")},
			{Name: "sort_by", Error: errors.New("unsupported column")},
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items?page_size=-1", nil)
	h(context.Background(), rr, req, valErr)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "validation" {
		t.Errorf("code = %v, want validation", body["code"])
	}
	if body["detail"] != "request validation failed" {
		t.Errorf("detail = %v, want 'request validation failed'", body["detail"])
	}
	params, ok := body["invalid-params"].([]any)
	if !ok || len(params) != 2 {
		t.Fatalf("invalid-params = %#v, want 2 items", body["invalid-params"])
	}
	assertInvalidParam(t, params[0], "page_size", "must be positive")
	assertInvalidParam(t, params[1], "sort_by", "unsupported column")
}

func TestErrorHandlerWithNegativeNoOptions(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger)

	valErr := &validate.Error{
		Fields: []validate.FieldError{
			{Name: "limit", Error: errors.New("too high")},
		},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	h(context.Background(), rr, req, valErr)

	wantStatus := ogenerrors.ErrorCode(valErr)
	if rr.Code != wantStatus {
		t.Errorf("status = %d, want %d from ogenerrors.ErrorCode", rr.Code, wantStatus)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, exists := body["invalid-params"]; exists {
		t.Errorf("invalid-params should not be present without WithInvalidParams()")
	}

	rrFallback := httptest.NewRecorder()
	unmappedErr := errors.New("unexpected error")
	h(context.Background(), rrFallback, req, unmappedErr)
	if rrFallback.Code != http.StatusInternalServerError {
		t.Errorf("unmapped fallback status = %d, want %d", rrFallback.Code, http.StatusInternalServerError)
	}
}

func TestErrorHandlerWithMappedServerErrorRedacted(t *testing.T) {
	t.Parallel()
	errSecretDB := errors.New("db auth failed on 10.1.2.3 with pass secret123")
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithStatus(errSecretDB, gerr.CodeInternal))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	h(context.Background(), rr, req, errSecretDB)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rr.Body.String(), "secret123") {
		t.Errorf("server error leaked secret: %s", rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["detail"] != "internal server error" {
		t.Errorf("detail = %v, want 'internal server error'", body["detail"])
	}
}

func TestErrorHandlerWithBoundaryEmptyFields(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithInvalidParams())

	emptyValErr := &validate.Error{Fields: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", nil)
	h(context.Background(), rr, req, emptyValErr)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
	if strings.Contains(rr.Body.String(), "invalid-params") {
		t.Errorf("expected no invalid-params key in raw JSON for empty fields, got: %s", rr.Body.String())
	}
}

func TestErrorHandlerWithFirstMatchOrdering(t *testing.T) {
	t.Parallel()
	errConflictOrBad := errors.New("ambiguous domain error")
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger,
		ogenkit.WithStatus(errConflictOrBad, gerr.CodeConflict),
		ogenkit.WithStatus(errConflictOrBad, gerr.CodeBadRequest),
	)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", nil)
	h(context.Background(), rr, req, errConflictOrBad)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d (first match should win)", rr.Code, http.StatusConflict)
	}
}

type publicStatusCase struct {
	name       string
	code       gerr.Code
	wantStatus int
	wantType   string
	wantTitle  string
}

func assertPublicProblem(t *testing.T, rr *httptest.ResponseRecorder, tc publicStatusCase, wantDetail string) {
	t.Helper()
	if rr.Code != tc.wantStatus {
		t.Errorf("status = %d, want %d", rr.Code, tc.wantStatus)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]any{
		"status":        float64(tc.wantStatus),
		"type":          tc.wantType,
		"title":         tc.wantTitle,
		"code":          string(tc.code),
		"instance":      "/media/queue",
		"detail":        wantDetail,
		"error_message": wantDetail,
		"message":       wantDetail,
	}
	for key, val := range want {
		if body[key] != val {
			t.Errorf("%s = %v, want %v", key, body[key], val)
		}
	}
}

func TestErrorHandlerWithPublicStatus(t *testing.T) {
	t.Parallel()
	errUpstream := errors.New("upstream connection reset")
	wrapped := fmt.Errorf("sonarr unreachable: %w", errUpstream)

	cases := []publicStatusCase{
		{
			name:       "unavailable 503",
			code:       gerr.CodeUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantType:   "https://golusoris.dev/errors/unavailable",
			wantTitle:  "Service Unavailable",
		},
		{
			name:       "timeout 504",
			code:       gerr.CodeTimeout,
			wantStatus: http.StatusGatewayTimeout,
			wantType:   "https://golusoris.dev/errors/timeout",
			wantTitle:  "Gateway Timeout",
		},
		{
			name:       "internal 500",
			code:       gerr.CodeInternal,
			wantStatus: http.StatusInternalServerError,
			wantType:   "https://golusoris.dev/errors/internal",
			wantTitle:  "Internal Server Error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
			h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithPublicStatus(errUpstream, tc.code))

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/media/queue", nil)
			h(context.Background(), rr, req, wrapped)

			assertPublicProblem(t, rr, tc, wrapped.Error())
		})
	}
}

func TestErrorHandlerWithStatusRedactsServerError(t *testing.T) {
	t.Parallel()
	errUpstream := errors.New("upstream connection reset")
	wrapped := fmt.Errorf("sonarr unreachable: %w", errUpstream)

	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithStatus(errUpstream, gerr.CodeUnavailable))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/queue", nil)
	h(context.Background(), rr, req, wrapped)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["detail"] != "internal server error" {
		t.Errorf("detail = %v, want 'internal server error'", body["detail"])
	}
	if body["error_message"] != "internal server error" {
		t.Errorf("error_message = %v, want 'internal server error'", body["error_message"])
	}
	if strings.Contains(rr.Body.String(), "sonarr unreachable") {
		t.Errorf("body leaked wrapped error: %s", rr.Body.String())
	}
}

func TestErrorHandlerUnmappedErrorRedacted500(t *testing.T) {
	t.Parallel()
	errUpstream := errors.New("upstream connection reset")
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithPublicStatus(errUpstream, gerr.CodeUnavailable))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/queue", nil)
	unmapped := errors.New("unexpected database leak")
	h(context.Background(), rr, req, unmapped)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["detail"] != "internal server error" {
		t.Errorf("detail = %v, want 'internal server error'", body["detail"])
	}
	if strings.Contains(rr.Body.String(), "database leak") {
		t.Errorf("body leaked unmapped error: %s", rr.Body.String())
	}
}

func TestErrorHandlerWithPublicStatus4xxUsesIncomingError(t *testing.T) {
	t.Parallel()
	errSentinel := errors.New("item not found")
	wrapped := fmt.Errorf("service layer: %w", errSentinel)

	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithPublicStatus(errSentinel, gerr.CodeNotFound))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items/123", nil)
	h(context.Background(), rr, req, wrapped)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantDetail := wrapped.Error()
	if body["detail"] != wantDetail {
		t.Errorf("detail = %v, want %q (incoming err.Error())", body["detail"], wantDetail)
	}
	if body["error_message"] != wantDetail {
		t.Errorf("error_message = %v, want %q", body["error_message"], wantDetail)
	}
}

func TestErrorHandlerWithPublicStatusNilTargetIgnored(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger, ogenkit.WithPublicStatus(nil, gerr.CodeUnavailable))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	errTest := errors.New("some error")
	h(context.Background(), rr, req, errTest)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["detail"] != "internal server error" {
		t.Errorf("detail = %v, want 'internal server error'", body["detail"])
	}
}

func TestErrorHandlerWithStatusPrecedenceOverPublicStatus(t *testing.T) {
	t.Parallel()
	errTarget := errors.New("backend failed")
	wrapped := fmt.Errorf("wrapper: %w", errTarget)

	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandlerWith(logger,
		ogenkit.WithStatus(errTarget, gerr.CodeUnavailable),
		ogenkit.WithPublicStatus(errTarget, gerr.CodeUnavailable),
	)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	h(context.Background(), rr, req, wrapped)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["detail"] != "internal server error" {
		t.Errorf("detail = %v, want 'internal server error' (WithStatus wins)", body["detail"])
	}
	if strings.Contains(rr.Body.String(), "backend failed") || strings.Contains(rr.Body.String(), "wrapper") {
		t.Errorf("body leaked error detail: %s", rr.Body.String())
	}
}

func TestErrorHandlerCacheControlNoStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		opts       []ogenkit.Option
		wantStatus int
	}{
		{
			name:       "4xx response",
			err:        gerr.NotFound("missing entity"),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "5xx response",
			err:        errors.New("unexpected crash"),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "422 invalid-params response",
			err: &validate.Error{
				Fields: []validate.FieldError{
					{Name: "field", Error: errors.New("invalid")},
				},
			},
			opts:       []ogenkit.Option{ogenkit.WithInvalidParams()},
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
			h := ogenkit.ErrorHandlerWith(logger, tc.opts...)

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			h(context.Background(), rr, req, tc.err)

			if rr.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
			}
		})
	}
}
