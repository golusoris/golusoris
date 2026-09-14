// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/idempotency"
)

// failingStore never finds a key and fails every Save with err.
type failingStore struct{ err error }

func (failingStore) Find(context.Context, string) (idempotency.CachedResponse, bool, error) {
	return idempotency.CachedResponse{}, false, nil
}

func (s failingStore) Save(context.Context, string, idempotency.CachedResponse, time.Duration) error {
	return s.err
}

func handler(body string, code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	})
}

func TestMiddleware_caches(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	h := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "created")
	}))

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Idempotency-Key", "key-1")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
		return rw
	}

	r1 := send()
	r2 := send()

	if calls.Load() != 1 {
		t.Fatalf("handler should be called once, got %d", calls.Load())
	}
	if r1.Code != http.StatusCreated || r2.Code != http.StatusCreated {
		t.Fatalf("both responses should be 201: %d %d", r1.Code, r2.Code)
	}
	if r1.Body.String() != "created" || r2.Body.String() != "created" {
		t.Fatalf("body mismatch: %q %q", r1.Body.String(), r2.Body.String())
	}
}

func TestMiddleware_noKey_passThrough(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	h := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	for range 3 {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls without key, got %d", calls.Load())
	}
}

func TestMiddleware_required(t *testing.T) {
	t.Parallel()
	h := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{Required: true},
	)(handler("ok", http.StatusOK))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rw.Code)
	}
}

func TestMiddleware_safeMethodSkipped(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	h := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	for range 3 {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Idempotency-Key", "key-get")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
	}
	if calls.Load() != 3 {
		t.Fatalf("GET should always pass through: got %d calls", calls.Load())
	}
}

func TestMiddleware_customHeader(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	h := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{Header: "X-Request-ID"},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-Request-ID", "custom-key-1")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
		return rw
	}

	r1 := send()
	r2 := send()

	if calls.Load() != 1 {
		t.Fatalf("handler should be called once with custom header, got %d", calls.Load())
	}
	if r1.Code != http.StatusAccepted || r2.Code != http.StatusAccepted {
		t.Fatalf("both responses should be 202: %d %d", r1.Code, r2.Code)
	}
}

func TestMiddleware_5xxNotCached(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	h := idempotency.Middleware(
		idempotency.NewMemoryStore(),
		idempotency.Options{},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Idempotency-Key", "err-key")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
	}
	if calls.Load() != 2 {
		t.Fatalf("5xx should not be cached: got %d calls", calls.Load())
	}
}

// TestMiddleware_saveFailure proves a Store.Save error neither blocks the
// response nor is dropped: the handler's reply is delivered and the failure is
// logged on Options.Logger.
func TestMiddleware_saveFailure(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := idempotency.Middleware(
		failingStore{err: errors.New("disk full")},
		idempotency.Options{Logger: logger},
	)(handler("created", http.StatusCreated))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Idempotency-Key", "key-1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusCreated || rw.Body.String() != "created" {
		t.Fatalf("response not delivered: %d %q", rw.Code, rw.Body.String())
	}
	if got := logs.String(); !strings.Contains(got, "idempotency: save response") || !strings.Contains(got, "disk full") {
		t.Fatalf("save failure not logged: %q", got)
	}
}
