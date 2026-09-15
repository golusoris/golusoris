// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIsSafeMethod_getIsSafe: GET is exempt from idempotency enforcement.
func TestIsSafeMethod_getIsSafe(t *testing.T) {
	t.Parallel()
	if !isSafeMethod(http.MethodGet) {
		t.Error("GET should be safe")
	}
}

// TestIsSafeMethod_headAndOptionsAreSafe covers the remaining safe methods
// in the same switch case as GET.
func TestIsSafeMethod_headAndOptionsAreSafe(t *testing.T) {
	t.Parallel()
	for _, m := range []string{http.MethodHead, http.MethodOptions} {
		if !isSafeMethod(m) {
			t.Errorf("%s should be safe", m)
		}
	}
}

// TestIsSafeMethod_postIsNotSafe: POST must be enforced.
func TestIsSafeMethod_postIsNotSafe(t *testing.T) {
	t.Parallel()
	if isSafeMethod(http.MethodPost) {
		t.Error("POST should not be safe")
	}
}

// TestHandleMissingKey_notRequired_passesThrough: without Required, a
// missing-key request reaches next.
func TestHandleMissingKey_notRequired_passesThrough(t *testing.T) {
	t.Parallel()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	handleMissingKey(rw, req, next, Options{Header: "Idempotency-Key"})
	if !called {
		t.Error("next should have been called")
	}
	if rw.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rw.Code, http.StatusOK)
	}
}

// TestHandleMissingKey_required_rejects: with Required, a missing-key
// request is rejected before reaching next.
func TestHandleMissingKey_required_rejects(t *testing.T) {
	t.Parallel()
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	rw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	handleMissingKey(rw, req, next, Options{Header: "Idempotency-Key", Required: true})
	if called {
		t.Error("next should not have been called")
	}
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rw.Code, http.StatusBadRequest)
	}
	if got := rw.Body.String(); got != "missing Idempotency-Key\n" {
		t.Errorf("body = %q", got)
	}
}

// TestCaptureResponse_capturesStatusHeaderAndBody proves captureResponse
// records exactly what next wrote, without touching the real ResponseWriter.
func TestCaptureResponse_capturesStatusHeaderAndBody(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("body"))
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	resp := captureResponse(next, req)
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusTeapot)
	}
	if resp.Header.Get("X-Test") != "yes" {
		t.Errorf("Header X-Test = %q, want yes", resp.Header.Get("X-Test"))
	}
	if string(resp.Body) != "body" {
		t.Errorf("Body = %q, want body", resp.Body)
	}
}

// recordingStore is a minimal [Store] fake for testing saveIfCacheable in
// isolation: Find always misses, Save records what it was called with.
type recordingStore struct {
	saved     bool
	saveErr   error
	lastResp  CachedResponse
	lastKey   string
	saveCalls int
}

func (s *recordingStore) Find(context.Context, string) (CachedResponse, bool, error) {
	return CachedResponse{}, false, nil
}

func (s *recordingStore) Save(_ context.Context, key string, resp CachedResponse, _ time.Duration) error {
	s.saved = true
	s.saveCalls++
	s.lastKey = key
	s.lastResp = resp
	return s.saveErr
}

// TestSaveIfCacheable_2xxIsSaved: a 2xx response is persisted under the
// given key, unchanged.
func TestSaveIfCacheable_2xxIsSaved(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	resp := CachedResponse{StatusCode: http.StatusOK, Body: []byte("ok")}
	saveIfCacheable(context.Background(), store, "k", resp, Options{Logger: slog.New(slog.DiscardHandler)})
	if !store.saved || store.saveCalls != 1 {
		t.Fatalf("expected exactly one Save call for a 2xx response, got %d", store.saveCalls)
	}
	if store.lastKey != "k" {
		t.Errorf("saved key = %q, want k", store.lastKey)
	}
	if string(store.lastResp.Body) != "ok" {
		t.Errorf("saved body = %q, want ok", store.lastResp.Body)
	}
}

// TestSaveIfCacheable_5xxIsNotSaved is the boundary: exactly 500 must not be
// cached so a transient failure can be retried.
func TestSaveIfCacheable_5xxIsNotSaved(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	saveIfCacheable(context.Background(), store, "k", CachedResponse{StatusCode: http.StatusInternalServerError}, Options{Logger: slog.New(slog.DiscardHandler)})
	if store.saved {
		t.Error("expected Save not to be called for a 5xx response")
	}
}

// TestSaveIfCacheable_499IsSaved is the boundary just below 500.
func TestSaveIfCacheable_499IsSaved(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	saveIfCacheable(context.Background(), store, "k", CachedResponse{StatusCode: 499}, Options{Logger: slog.New(slog.DiscardHandler)})
	if !store.saved {
		t.Error("expected Save to be called for status 499")
	}
}

// TestSaveIfCacheable_saveErrorIsLogged proves a Save failure is logged
// rather than propagated.
func TestSaveIfCacheable_saveErrorIsLogged(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	store := &recordingStore{saveErr: errors.New("disk full")}
	saveIfCacheable(context.Background(), store, "k", CachedResponse{StatusCode: http.StatusOK}, Options{Logger: logger})
	if got := logs.String(); !strings.Contains(got, "idempotency: save response") || !strings.Contains(got, "disk full") {
		t.Errorf("save failure not logged: %q", got)
	}
}
