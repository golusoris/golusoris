package idempotency_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/golusoris/golusoris/idempotency"
)

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
