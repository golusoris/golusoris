// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ogenkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ogenmw "github.com/ogen-go/ogen/middleware"
	"github.com/ogen-go/ogen/ogenerrors"

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

func TestErrorHandlerHidesCodedServerErrorDetail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{name: "internal", err: gerr.Internal("database password leaked"), want: http.StatusInternalServerError},
		{name: "unavailable", err: gerr.New(gerr.CodeUnavailable, "upstream token leaked"), want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
			h := ogenkit.ErrorHandler(logger)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/widgets", nil)

			h(context.Background(), rr, req, tc.err)

			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d", rr.Code, tc.want)
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
