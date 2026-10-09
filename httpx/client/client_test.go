// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/httpx/client"
)

type typedNilTracerProvider struct{ trace.TracerProvider }

func TestNewDefaults(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "negative defaults", timeout: -time.Nanosecond, want: 30 * time.Second},
		{name: "zero defaults", timeout: 0, want: 30 * time.Second},
		{name: "positive preserved", timeout: time.Nanosecond, want: time.Nanosecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c := client.New(client.Options{Timeout: test.timeout})
			if c.Timeout != test.want {
				t.Errorf("Timeout = %v, want %v", c.Timeout, test.want)
			}
		})
	}
}

func TestNewTypedNilTracerProviderUsesGlobal(t *testing.T) {
	t.Parallel()
	var provider *typedNilTracerProvider
	got := client.New(client.Options{TracerProvider: provider})
	if got == nil || got.Transport == nil {
		t.Fatal("New returned an unusable client")
	}
}

func TestCloneBounded(t *testing.T) {
	t.Parallel()
	transport := http.DefaultTransport
	source := &http.Client{Transport: transport}
	clone := client.CloneBounded(source, 7*time.Second)
	if clone == source {
		t.Fatal("CloneBounded returned caller-owned client")
	}
	if source.Timeout != 0 || clone.Timeout != 7*time.Second {
		t.Fatalf("source/clone timeout = %v/%v, want 0/7s", source.Timeout, clone.Timeout)
	}
	if clone.Transport != source.Transport {
		t.Fatal("CloneBounded replaced caller transport")
	}

	positive := &http.Client{Timeout: time.Second}
	if got := client.CloneBounded(positive, 7*time.Second).Timeout; got != time.Second {
		t.Fatalf("positive timeout = %v, want 1s", got)
	}
	if got := client.CloneBounded(nil, 7*time.Second).Timeout; got != 7*time.Second {
		t.Fatalf("nil-source timeout = %v, want 7s", got)
	}
	if got := client.CloneBounded(nil, -time.Second).Timeout; got != 30*time.Second {
		t.Fatalf("default timeout = %v, want 30s", got)
	}
}

// TestCloneBoundedOwnsMissingTransport pins that a client without a transport
// never shares http.DefaultTransport, whose idle connections any code may close.
func TestCloneBoundedOwnsMissingTransport(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]*http.Client{
		"nil source":    nil,
		"nil transport": {Timeout: time.Second},
	} {
		got := client.CloneBounded(source, 7*time.Second).Transport
		if _, ok := got.(*http.Transport); !ok || got == http.DefaultTransport {
			t.Fatalf("%s: transport = %T shared=%v, want a private *http.Transport", name, got, got == http.DefaultTransport)
		}
	}
	first := client.CloneBounded(nil, time.Second).Transport
	if second := client.CloneBounded(nil, time.Second).Transport; first == second {
		t.Fatal("two clients share one transport")
	}
}

// TestNewOwnsIdlePool pins that a client built without a transport keeps its
// idle connection when other code closes http.DefaultTransport's pool (#703).
func TestNewOwnsIdlePool(t *testing.T) {
	t.Parallel()
	var dials atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			dials.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	c := client.New(client.Options{})
	for i := range 2 {
		if i == 1 {
			shared, ok := http.DefaultTransport.(*http.Transport)
			if !ok {
				t.Fatalf("http.DefaultTransport = %T, want *http.Transport", http.DefaultTransport)
			}
			shared.CloseIdleConnections()
		}
		if _, err := getBody(t, c, srv.URL); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("dials = %d, want 1: the client shares http.DefaultTransport's idle pool", got)
	}
}

// idleCountingTransport is a countingTransport that counts CloseIdleConnections.
type idleCountingTransport struct {
	countingTransport
	closes atomic.Int32
}

func (c *idleCountingTransport) CloseIdleConnections() { c.closes.Add(1) }

// TestCloseIdleConnectionsReachesTransport pins that every layer forwards
// CloseIdleConnections to the innermost transport and tolerates one without
// the method (#709).
func TestCloseIdleConnectionsReachesTransport(t *testing.T) {
	t.Parallel()
	layers := map[string]client.Options{
		"plain":         {},
		"retry":         {Retry: client.RetryOptions{Max: 1}},
		"breaker":       {Breaker: client.BreakerOptions{Max: 1}},
		"retry+breaker": {Retry: client.RetryOptions{Max: 1}, Breaker: client.BreakerOptions{Max: 1}},
	}
	for name, opts := range layers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := &idleCountingTransport{}
			opts.Transport = tr
			client.New(opts).CloseIdleConnections()
			if got := tr.closes.Load(); got != 1 {
				t.Fatalf("inner CloseIdleConnections calls = %d, want 1", got)
			}
			opts.Transport = &countingTransport{}
			client.New(opts).CloseIdleConnections() // must not panic
		})
	}
}

// TestCloseIdleConnectionsEmptiesOwnPool pins that the private pool drops its
// idle connection when the client closes idle connections (#709).
func TestCloseIdleConnectionsEmptiesOwnPool(t *testing.T) {
	t.Parallel()
	var dials atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			dials.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	c := client.New(client.Options{Retry: client.RetryOptions{Max: 1}, Breaker: client.BreakerOptions{Max: 1}})
	for i := range 2 {
		if _, err := getBody(t, c, srv.URL); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		c.CloseIdleConnections()
	}
	if got := dials.Load(); got != 2 {
		t.Fatalf("dials = %d, want 2: CloseIdleConnections left the idle connection open", got)
	}
}

// TestRetryExhaustionKeepsIdlePool pins that a failed retry sequence leaves the
// pool shared by concurrent requests open (#709).
func TestRetryExhaustionKeepsIdlePool(t *testing.T) {
	t.Parallel()
	tr := &idleCountingTransport{countingTransport: countingTransport{failures: 2}}
	c := client.New(client.Options{
		Transport: tr,
		Retry:     client.RetryOptions{Max: 1, Wait: time.Millisecond, MaxWait: time.Millisecond},
	})
	if _, err := getBody(t, c, "http://upstream.invalid/"); err != nil {
		t.Fatalf("Get() error = %v, want final 503 response", err)
	}
	if calls, closes := tr.calls.Load(), tr.closes.Load(); calls != 2 || closes != 0 {
		t.Fatalf("calls/closes = %d/%d, want 2/0", calls, closes)
	}
}

func TestReadAllBounded(t *testing.T) {
	t.Parallel()
	data, err := client.ReadAllBounded(strings.NewReader("1234"), 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("exact boundary = (%q, %v)", data, err)
	}
	if _, err = client.ReadAllBounded(strings.NewReader("12345"), 4); !errors.Is(err, client.ErrBodyTooLarge) {
		t.Fatalf("overflow error = %v, want ErrBodyTooLarge", err)
	}
	for _, maxBytes := range []int64{0, -1, int64(^uint64(0) >> 1)} {
		if _, err = client.ReadAllBounded(strings.NewReader("x"), maxBytes); err == nil {
			t.Fatalf("limit %d accepted", maxBytes)
		}
	}
	if _, err = client.ReadAllBounded(nil, 1); err == nil {
		t.Fatal("nil reader accepted")
	}
	var typedNilReader *strings.Reader
	if _, err = client.ReadAllBounded(typedNilReader, 1); err == nil {
		t.Fatal("typed-nil reader accepted")
	}
}

func TestRetryDoesNotRepeatUnsafeMethodsByDefault(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "retryable status", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := client.New(client.Options{
		Retry: client.RetryOptions{Max: 3, Wait: time.Millisecond, MaxWait: time.Millisecond},
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("side effect"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if attempts.Load() != 1 {
		t.Fatalf("POST attempts = %d, want 1", attempts.Load())
	}
}

func TestRetryDoesNotTreatBlankIdempotencyKeyAsReplayProof(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if key := r.Header.Get("Idempotency-Key"); key != "" {
			t.Errorf("upstream Idempotency-Key = %q, want empty", key)
		}
		http.Error(w, "retryable status", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := client.New(client.Options{
		Retry: client.RetryOptions{Max: 3, Wait: time.Millisecond, MaxWait: time.Millisecond},
	})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader("side effect"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Idempotency-Key", " \t ")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if attempts.Load() != 1 {
		t.Fatalf("POST attempts = %d, want 1", attempts.Load())
	}
}

func TestRetryAllowsUnsafeMethodWithIdempotencyKey(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != int64(len("side effect")) {
			t.Errorf("ContentLength = %d, want %d", r.ContentLength, len("side effect"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if string(body) != "side effect" {
			t.Errorf("request body = %q, want %q", body, "side effect")
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := client.New(client.Options{
		Retry: client.RetryOptions{Max: 2, Wait: time.Millisecond, MaxWait: time.Millisecond},
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("side effect"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	originalBody := &trackingRequestBody{reader: strings.NewReader("side effect")}
	req.Body = originalBody
	req.Header.Set("Idempotency-Key", "operation-1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if attempts.Load() != 2 {
		t.Fatalf("POST attempts = %d, want 2", attempts.Load())
	}
	if originalBody.reads != 0 || originalBody.closes != 1 {
		t.Fatalf("original body reads/closes = %d/%d, want 0/1", originalBody.reads, originalBody.closes)
	}
}

func TestRetryRejectsNonReplayableBodyWithoutReading(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	body := &trackingRequestBody{reader: strings.NewReader("side effect")}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Idempotency-Key", "operation-1")
	if req.GetBody != nil {
		t.Fatal("test request unexpectedly has GetBody")
	}
	c := client.New(client.Options{
		Retry: client.RetryOptions{Max: 2, Wait: time.Millisecond, MaxWait: time.Millisecond},
	})
	resp, err := c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, client.ErrBodyNotReplayable) {
		t.Fatalf("Do() error = %v, want non-replayable-body error", err)
	}
	if attempts.Load() != 0 {
		t.Fatalf("upstream attempts = %d, want 0", attempts.Load())
	}
	if body.reads != 0 || body.closes != 1 {
		t.Fatalf("body reads/closes = %d/%d, want 0/1", body.reads, body.closes)
	}
}

func TestRetryReturnsFinalResponseAfterExhaustion(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, fmt.Sprintf("attempt-%d", attempt))
	}))
	defer srv.Close()
	c := client.New(client.Options{
		Retry: client.RetryOptions{Max: 2, Wait: time.Millisecond, MaxWait: time.Millisecond},
	})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get() error = %v, want final response", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read final response: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "attempt-3" {
		t.Fatalf("final response = %d %q, want 503 %q", resp.StatusCode, body, "attempt-3")
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestRetryRejectsBrokenGetBodyBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		getBody func() (io.ReadCloser, error)
	}{
		{
			name: "error",
			getBody: func() (io.ReadCloser, error) {
				return nil, errors.New("factory failed")
			},
		},
		{
			name: "nil body",
			getBody: func() (io.ReadCloser, error) {
				return nil, nil //nolint:nilnil // Deliberately violates GetBody contract.
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			body := &trackingRequestBody{reader: strings.NewReader("body")}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, srv.URL, body)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.GetBody = tt.getBody
			c := client.New(client.Options{
				Retry: client.RetryOptions{Max: 1, Wait: time.Millisecond, MaxWait: time.Millisecond},
			})
			resp, err := c.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if !errors.Is(err, client.ErrBodyNotReplayable) {
				t.Fatalf("Do() error = %v, want ErrBodyNotReplayable", err)
			}
			if attempts.Load() != 0 || body.reads != 0 || body.closes != 1 {
				t.Fatalf(
					"attempts/reads/closes = %d/%d/%d, want 0/0/1",
					attempts.Load(),
					body.reads,
					body.closes,
				)
			}
		})
	}
}

type trackingRequestBody struct {
	reader *strings.Reader
	reads  int
	closes int
}

func (b *trackingRequestBody) Read(dst []byte) (int, error) {
	b.reads++
	return b.reader.Read(dst)
}

func (b *trackingRequestBody) Close() error {
	b.closes++
	return nil
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
	defer func() { _ = resp.Body.Close() }()

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

func TestOpenBreakerClosesRejectedRequestBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "dead", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := client.New(client.Options{
		Breaker: client.BreakerOptions{Max: 1, OpenFor: time.Minute},
	})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("trip breaker: %v", err)
	}
	_ = resp.Body.Close()

	body := &trackingRequestBody{reader: strings.NewReader("secret payload")}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err = c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "circuit open") {
		t.Fatalf("Do() error = %v, want circuit open", err)
	}
	if body.reads != 0 || body.closes != 1 {
		t.Fatalf("rejected body reads/closes = %d/%d, want 0/1", body.reads, body.closes)
	}
}

func TestDrainIsNilSafe(t *testing.T) {
	t.Parallel()
	var body *trackingRequestBody
	response := &http.Response{Body: body}

	// Should not panic on nil or typed-nil inputs.
	client.Drain(nil, nil) //nolint:staticcheck // intentional: nil-safe contract
	client.Drain(t.Context(), response)
}
