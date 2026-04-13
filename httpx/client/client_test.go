package client_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/httpx/client"
)

func TestNewDefaults(t *testing.T) {
	t.Parallel()
	c := client.New(client.Options{})
	if c.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v", c.Timeout)
	}
}

// TestRetryRecoversFromTransientFailure proves the retry layer retries 5xx.
func TestRetryRecoversFromTransientFailure(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "oops", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c := client.New(client.Options{
		Timeout: 5 * time.Second,
		Retry:   client.RetryOptions{Max: 5, Wait: 1 * time.Millisecond, MaxWait: 2 * time.Millisecond},
	})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

// TestBreakerOpensAfterFailures proves the breaker stops hitting a dead
// endpoint once the failure threshold is reached.
func TestBreakerOpensAfterFailures(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "dead", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := client.New(client.Options{
		Timeout: 5 * time.Second,
		Breaker: client.BreakerOptions{Max: 2, OpenFor: time.Minute},
	})
	// First two requests land, third should trip -> short-circuit.
	for range 5 {
		resp, err := c.Get(srv.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		_ = err
	}
	// Breaker should be open; subsequent call returns without reaching srv.
	before := attempts.Load()
	resp, err := c.Get(srv.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	after := attempts.Load()
	if after != before {
		t.Errorf("breaker did not open: before=%d after=%d", before, after)
	}
	if err == nil || !strings.Contains(err.Error(), "circuit open") {
		t.Errorf("expected circuit open error, got: %v", err)
	}
}

func TestDrainIsNilSafe(t *testing.T) {
	t.Parallel()
	// Should not panic on nil.
	client.Drain(nil, nil) //nolint:staticcheck // intentional: nil-safe contract
}
