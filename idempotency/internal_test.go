// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsSafeMethod(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		http.MethodGet:     true,
		http.MethodHead:    true,
		http.MethodOptions: true,
		http.MethodPost:    false,
	}
	for method, want := range tests {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			if got := isSafeMethod(method); got != want {
				t.Fatalf("isSafeMethod(%q) = %v; want %v", method, got, want)
			}
		})
	}
}

func TestHandleMissingKey(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		required bool
		status   int
		called   bool
	}{
		"optional passes through": {status: http.StatusNoContent, called: true},
		"required rejects":        {required: true, status: http.StatusBadRequest},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			handleMissingKey(
				recorder,
				httptest.NewRequest(http.MethodPost, "/", nil),
				next,
				Options{Header: "Idempotency-Key", Required: tc.required},
			)
			if recorder.Code != tc.status || called != tc.called {
				t.Fatalf("response = (%d, called %v); want (%d, called %v)", recorder.Code, called, tc.status, tc.called)
			}
		})
	}
}

func TestCaptureResponse_CapturesStatusHeaderAndBody(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("body"))
	})
	target := httptest.NewRecorder()
	response, overflow := captureResponse(
		target,
		next,
		httptest.NewRequest(http.MethodPost, "/", nil),
		defaultBodyLimit,
	)
	if overflow {
		t.Fatal("small response unexpectedly overflowed capture limit")
	}
	if response.StatusCode != http.StatusTeapot || response.Header.Get("X-Test") != "yes" || string(response.Body) != "body" {
		t.Fatalf("captured response = %+v; want 418, X-Test=yes, body", response)
	}
	if target.Code != http.StatusTeapot || target.Body.String() != "body" {
		t.Fatalf("target response = %d %q; want 418 body", target.Code, target.Body)
	}
}

func TestCaptureResponse_BoundsCaptureWithoutTruncatingClient(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("six!!!"))
	})
	target := httptest.NewRecorder()
	response, overflow := captureResponse(
		target,
		next,
		httptest.NewRequest(http.MethodPost, "/", nil),
		5,
	)
	if !overflow {
		t.Fatal("capture did not report overflow")
	}
	if got := len(response.Body); got != 5 {
		t.Fatalf("captured bytes = %d; want 5", got)
	}
	if got := target.Body.String(); got != "six!!!" {
		t.Fatalf("client body = %q; want full response", got)
	}
}

func TestFingerprintRequest_RestoresBodyAndEnforcesLimit(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	fingerprint, err := fingerprintRequest(request, 4)
	if err != nil {
		t.Fatalf("fingerprintRequest: %v", err)
	}
	if fingerprint == "" {
		t.Fatal("fingerprintRequest returned empty fingerprint")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(body) != "body" {
		t.Fatalf("restored body = %q; want body", body)
	}

	tooLarge := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("six!!!"))
	if _, err := fingerprintRequest(tooLarge, 5); !errors.Is(err, ErrRequestBodyTooLarge) {
		t.Fatalf("fingerprintRequest oversized error = %v; want ErrRequestBodyTooLarge", err)
	}
}

func TestScopedKey_CanonicalizesHostAndQueryOrder(t *testing.T) {
	t.Parallel()
	first := httptest.NewRequest(http.MethodPost, "https://API.EXAMPLE/payments?b=2&a=1", nil)
	second := httptest.NewRequest(http.MethodPost, "https://api.example/payments?a=1&b=2", nil)
	firstKey, err := scopedKey(first, "request-key", nil)
	if err != nil {
		t.Fatalf("scopedKey first: %v", err)
	}
	secondKey, err := scopedKey(second, "request-key", nil)
	if err != nil {
		t.Fatalf("scopedKey second: %v", err)
	}
	if firstKey != secondKey {
		t.Fatalf("canonical keys differ: %q != %q", firstKey, secondKey)
	}
}

func TestOptionsDefaultsReplaceUnsafeNonPositiveBounds(t *testing.T) {
	t.Parallel()
	opts := Options{
		TTL:             -time.Second,
		MaxRequestBody:  -1,
		MaxResponseBody: -1,
	}
	opts.defaults()
	if opts.TTL != 24*time.Hour || opts.MaxRequestBody != defaultBodyLimit || opts.MaxResponseBody != defaultBodyLimit {
		t.Fatalf("defaults = %+v; want finite positive retention and body bounds", opts)
	}
}

func TestMemoryStoreRejectsInvalidReservationArguments(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()
	if _, err := store.Claim(ctx, "", "fingerprint", time.Minute); err == nil {
		t.Fatal("Claim accepted an empty key")
	}
	if _, err := store.Claim(ctx, "key", "fingerprint", -time.Second); err == nil {
		t.Fatal("Claim accepted a negative TTL")
	}
	if err := store.Commit(ctx, "key", "", "fingerprint", CachedResponse{}, time.Minute); err == nil {
		t.Fatal("Commit accepted an empty token")
	}
	if err := store.Release(ctx, "key", ""); err == nil {
		t.Fatal("Release accepted an empty token")
	}
}
