// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency"
)

type typedNilStore struct{}

func (s *typedNilStore) Claim(context.Context, string, string, time.Duration) (idempotency.ClaimResult, error) {
	if s == nil {
		panic("typed-nil store dereferenced")
	}
	return idempotency.ClaimResult{}, nil
}

func (s *typedNilStore) Commit(
	context.Context,
	string,
	string,
	string,
	idempotency.CachedResponse,
	time.Duration,
) error {
	if s == nil {
		panic("typed-nil store dereferenced")
	}
	return nil
}

func (s *typedNilStore) Release(context.Context, string, string) error {
	if s == nil {
		panic("typed-nil store dereferenced")
	}
	return nil
}

// failingStore never finds a key and fails every Save with err.
type failingStore struct{ err error }

func (failingStore) Claim(context.Context, string, string, time.Duration) (idempotency.ClaimResult, error) {
	return idempotency.ClaimResult{State: idempotency.ClaimAcquired, Token: "token"}, nil
}

func (s failingStore) Commit(
	context.Context,
	string,
	string,
	string,
	idempotency.CachedResponse,
	time.Duration,
) error {
	return s.err
}

func (failingStore) Release(context.Context, string, string) error { return nil }

// findFailingStore fails every Find with err; Save is never expected to be
// reached from these tests.
type findFailingStore struct{ err error }

func (s findFailingStore) Claim(context.Context, string, string, time.Duration) (idempotency.ClaimResult, error) {
	return idempotency.ClaimResult{}, s.err
}

func (findFailingStore) Commit(
	context.Context,
	string,
	string,
	string,
	idempotency.CachedResponse,
	time.Duration,
) error {
	return nil
}

func (findFailingStore) Release(context.Context, string, string) error { return nil }

type fixedClaimStore struct {
	claim idempotency.ClaimResult
}

func (s fixedClaimStore) Claim(context.Context, string, string, time.Duration) (idempotency.ClaimResult, error) {
	return s.claim, nil
}

func (fixedClaimStore) Commit(
	context.Context,
	string,
	string,
	string,
	idempotency.CachedResponse,
	time.Duration,
) error {
	return nil
}

func (fixedClaimStore) Release(context.Context, string, string) error { return nil }

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

func TestMiddleware_TypedNilStoreFailsClosed(t *testing.T) {
	t.Parallel()
	var store *typedNilStore
	called := false
	h := idempotency.Middleware(store, idempotency.Options{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Idempotency-Key", "key-1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if called {
		t.Fatal("next called without a usable idempotency store")
	}
	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want %d", rw.Code, http.StatusInternalServerError)
	}
}

func TestNewMemoryStoreWithClock_TypedNilFallsBack(t *testing.T) {
	t.Parallel()
	var clk *clockwork.FakeClock
	store := idempotency.NewMemoryStoreWithClock(clk)
	ctx := context.Background()
	claim, err := store.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	err = store.Commit(
		ctx,
		"key",
		claim.Token,
		"fingerprint",
		idempotency.CachedResponse{StatusCode: http.StatusCreated},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	completed, err := store.Claim(ctx, "key", "fingerprint", time.Minute)
	if err != nil || completed.State != idempotency.ClaimCompleted {
		t.Fatalf("Claim = (%+v, %v); want completed response", completed, err)
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

// TestMiddleware_findFailure proves a Store.Find error is reported as a 500
// and never reaches the wrapped handler.
func TestMiddleware_findFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	h := idempotency.Middleware(
		findFailingStore{err: errors.New("store unavailable")},
		idempotency.Options{},
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Idempotency-Key", "key-1")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusInternalServerError)
	}
	if calls.Load() != 0 {
		t.Fatalf("handler should not be called on store error, got %d calls", calls.Load())
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
	if got := logs.String(); !strings.Contains(got, "idempotency: commit response") || !strings.Contains(got, "disk full") {
		t.Fatalf("commit failure not logged: %q", got)
	}
}

func TestMiddlewareRejectsMalformedStoreClaims(t *testing.T) {
	t.Parallel()
	tests := map[string]idempotency.ClaimResult{
		"acquired without token": {State: idempotency.ClaimAcquired},
		"completed invalid status": {
			State:       idempotency.ClaimCompleted,
			Fingerprint: emptyRequestFingerprint(),
			Response:    idempotency.CachedResponse{StatusCode: -1},
		},
	}
	for name, claim := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := idempotency.Middleware(fixedClaimStore{claim: claim}, idempotency.Options{})(http.HandlerFunc(
				func(http.ResponseWriter, *http.Request) { called = true },
			))
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("Idempotency-Key", "key")
			rw := httptest.NewRecorder()
			requireNoPanic(t, func() { h.ServeHTTP(rw, req) })
			if called || rw.Code != http.StatusInternalServerError {
				t.Fatalf("response = (%d, called %v); want 500 and no handler", rw.Code, called)
			}
		})
	}
}

func emptyRequestFingerprint() string {
	// fingerprintPayload prefixes both empty parts with an eight-byte length.
	sum := sha256.Sum256(make([]byte, 16))
	return hex.EncodeToString(sum[:])
}

func requireNoPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("panicked: %v", recovered)
		}
	}()
	fn()
}
