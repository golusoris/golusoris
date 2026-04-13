package ogenkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	ogenmw "github.com/ogen-go/ogen/middleware"

	gerr "github.com/golusoris/golusoris/errors"
	"github.com/golusoris/golusoris/ogenkit"
)

func TestErrorHandlerMapsGolusorisError(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandler(logger)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h(context.Background(), rr, req, gerr.NotFound("user"))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code = %q", body["code"])
	}
	if body["message"] != "user" {
		t.Errorf("message = %q", body["message"])
	}
}

func TestErrorHandlerFallsBackToOgenDefault(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
	h := ogenkit.ErrorHandler(logger)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h(context.Background(), rr, req, errors.New("random failure"))

	// Ogen default writes 500 for unclassified errors.
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
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
