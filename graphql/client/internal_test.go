// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/config"
)

func TestWithDefaults_zeroFillsTimeout(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, defaultTimeout)
	}
}

func TestWithDefaults_preservesTimeout(t *testing.T) {
	t.Parallel()
	c := Config{Timeout: 5 * time.Second}.withDefaults()
	if c.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", c.Timeout)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v", c.Timeout)
	}
}

func TestNewClient_noEndpoint(t *testing.T) {
	t.Parallel()
	_, err := newClient(Config{})
	if err == nil {
		t.Error("expected error for empty endpoint")
	}
}

func TestNewClient_withEndpoint(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c, err := newClient(Config{Endpoint: srv.URL, Timeout: time.Second})
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c == nil {
		t.Error("expected non-nil client")
	}
}

// TestNewHTTPClient_ownsTransport pins that every client wraps its own clone
// of http.DefaultTransport, never the shared pool (#703).
func TestNewHTTPClient_ownsTransport(t *testing.T) {
	t.Parallel()
	base := func(hc *http.Client) http.RoundTripper {
		t.Helper()
		auth, ok := hc.Transport.(authTransport)
		if !ok {
			t.Fatalf("transport = %T, want authTransport", hc.Transport)
		}
		return auth.base
	}
	first := base(newHTTPClient(Config{Timeout: time.Second}))
	if _, ok := first.(*http.Transport); !ok || first == http.DefaultTransport {
		t.Fatalf("base = %T shared=%v, want a private *http.Transport", first, first == http.DefaultTransport)
	}
	if second := base(newHTTPClient(Config{Timeout: time.Second})); second == first {
		t.Fatal("two clients share one transport")
	}
}

// TestNewHTTPClient_timeout covers the explicit and zero-timeout boundaries.
func TestNewHTTPClient_timeout(t *testing.T) {
	t.Parallel()
	if got := newHTTPClient(Config{Timeout: time.Second}).Timeout; got != time.Second {
		t.Errorf("explicit Timeout = %v, want 1s", got)
	}
	if got := newHTTPClient(Config{}).Timeout; got != defaultTimeout {
		t.Errorf("zero Timeout = %v, want %v", got, defaultTimeout)
	}
}

func TestRoundTrip_injectsAuth(t *testing.T) {
	t.Parallel()
	var gotAuth, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("X-Api-Key")
	}))
	t.Cleanup(srv.Close)

	tr := authTransport{
		base:        srv.Client().Transport,
		bearerToken: "tok",
		apiKey:      "key123",
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, _ := tr.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotKey != "key123" {
		t.Errorf("X-Api-Key = %q", gotKey)
	}
}
