// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ratelimit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"

	"github.com/golusoris/golusoris/httpx/ratelimit"
)

type recordingStore struct {
	limiter.Store
	gets atomic.Int64
}

func (s *recordingStore) Get(
	ctx context.Context,
	key string,
	rate limiter.Rate,
) (limiter.Context, error) {
	s.gets.Add(1)
	return s.Store.Get(ctx, key, rate)
}

func TestEmptyRateIsNoop(t *testing.T) {
	t.Parallel()
	mw, err := ratelimit.New(ratelimit.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for range 100 {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr.Code != http.StatusTeapot {
			t.Fatalf("status = %d", rr.Code)
		}
	}
}

func TestRateEnforced(t *testing.T) {
	t.Parallel()
	mw, err := ratelimit.New(ratelimit.Options{Rate: "2-S"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	statuses := make([]int, 0, 4)
	for range 4 {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(rr, req)
		statuses = append(statuses, rr.Code)
	}
	// First two should be 200, last two 429.
	if statuses[0] != 200 || statuses[1] != 200 {
		t.Errorf("first two should pass, got %v", statuses)
	}
	if statuses[2] != http.StatusTooManyRequests || statuses[3] != http.StatusTooManyRequests {
		t.Errorf("third/fourth should be 429, got %v", statuses)
	}
}

func TestConfiguredStoreIsUsed(t *testing.T) {
	t.Parallel()
	store := &recordingStore{Store: memory.NewStore()}
	mw, err := ratelimit.New(ratelimit.Options{Rate: "2-S", Store: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := store.gets.Load(); got != 1 {
		t.Fatalf("store Get calls = %d, want 1", got)
	}
}

func TestTypedNilStoreIsRejected(t *testing.T) {
	t.Parallel()
	var store *recordingStore
	if _, err := ratelimit.New(ratelimit.Options{Rate: "2-S", Store: store}); err == nil {
		t.Fatal("New accepted typed-nil store")
	}
}

func TestBadRateErrors(t *testing.T) {
	t.Parallel()
	_, err := ratelimit.New(ratelimit.Options{Rate: "not-a-rate"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestTrustXFFIsRejected(t *testing.T) {
	t.Parallel()
	if _, err := ratelimit.New(ratelimit.Options{Rate: "2-S", TrustXFF: true}); err == nil {
		t.Fatal("TrustXFF=true succeeded")
	}
}
