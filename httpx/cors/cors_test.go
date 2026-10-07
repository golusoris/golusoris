// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golusoris/golusoris/httpx/cors"
)

func TestPreflightAllowsConfiguredOrigin(t *testing.T) {
	t.Parallel()
	mw := newMiddleware(t, cors.Options{
		Origins: []string{"https://app.example"},
		Methods: []string{http.MethodGet, http.MethodPost},
		Headers: []string{"Content-Type"},
	})
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Allow-Origin = %q", got)
	}
}

func TestBlocksUnlistedOrigin(t *testing.T) {
	t.Parallel()
	mw := newMiddleware(t, cors.Options{Origins: []string{"https://app.example"}})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Request passes through (handler runs), but Allow-Origin is not set.
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Allow-Origin should be empty for disallowed origin")
	}
}

func TestDefaultDeniesCrossOrigin(t *testing.T) {
	t.Parallel()
	middleware := newMiddleware(t, cors.Options{})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		request := httptest.NewRequest(method, "/", nil)
		request.Header.Set("Origin", "https://evil.example")
		if method == http.MethodOptions {
			request.Header.Set("Access-Control-Request-Method", http.MethodGet)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s Access-Control-Allow-Origin = %q, want empty", method, got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != "" {
			t.Fatalf("%s Access-Control-Allow-Methods = %q, want empty", method, got)
		}
	}
}

func TestNewSnapshotsAllowedMethods(t *testing.T) {
	t.Parallel()
	methods := []string{http.MethodGet}
	middleware := newMiddleware(t, cors.Options{
		Origins: []string{"https://app.example"},
		Methods: methods,
	})
	methods[0] = http.MethodPost
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want configured origin", got)
	}
}

func TestNewRejectsCredentialedMatchAllOrigin(t *testing.T) {
	t.Parallel()

	for _, origins := range [][]string{{"*"}, {"https://app.example", "*"}} {
		if _, err := cors.New(cors.Options{Origins: origins, Credentials: true}); err == nil {
			t.Fatalf("New(%v) error = nil, want invalid credentialed wildcard error", origins)
		}
	}
	if _, err := cors.New(cors.Options{
		Origins:     []string{"https://*.example"},
		Credentials: true,
	}); err != nil {
		t.Fatalf("New(partial wildcard) error = %v", err)
	}
}

func TestNegativeMaxAgeForcesZeroCaching(t *testing.T) {
	t.Parallel()

	middleware := newMiddleware(t, cors.Options{
		Origins: []string{"https://app.example"},
		Methods: []string{http.MethodGet},
		MaxAge:  -time.Nanosecond,
	})
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if got := recorder.Header().Get("Access-Control-Max-Age"); got != "0" {
		t.Fatalf("Access-Control-Max-Age = %q, want 0", got)
	}
}

func newMiddleware(t *testing.T, opts cors.Options) func(http.Handler) http.Handler {
	t.Helper()
	middleware, err := cors.New(opts)
	if err != nil {
		t.Fatalf("cors.New() error = %v", err)
	}
	return middleware
}
