// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"github.com/ulule/limiter/v3"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/testutil/httpclient"
)

type scriptedStore struct {
	limiter.Store
	get func(context.Context, string, limiter.Rate) (limiter.Context, error)
}

func (s *scriptedStore) Get(ctx context.Context, key string, rate limiter.Rate) (limiter.Context, error) {
	return s.get(ctx, key, rate)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestOutboundRates(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	var calls atomic.Int32
	store := &scriptedStore{get: func(_ context.Context, key string, rate limiter.Rate) (limiter.Context, error) {
		require.Equal(t, "outbound:example.com", key)
		require.EqualValues(t, 2, rate.Limit)
		return limiter.Context{}, nil
	}}
	out, err := NewOutbound(TransportOptions{Rate: "1-S", Hosts: []HostRate{{Host: "EXAMPLE.com", Rate: "2-S"}}, Store: store}, clk)
	require.NoError(t, err)
	next := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{Body: http.NoBody}, nil
	})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://Example.Com:8080/data", nil)
	require.NoError(t, err)
	resp, err := out.Wrap(next).RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.EqualValues(t, 1, calls.Load())
}

func TestOutboundLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                string
		maxWait, reset      time.Duration
		wait, cancel, twice bool
	}{
		{name: "fail fast", reset: time.Second},
		{name: "beyond budget", maxWait: time.Second, reset: 2 * time.Second},
		{name: "reset boundary", maxWait: time.Second, reset: time.Second, wait: true},
		{name: "cancel wait", maxWait: time.Second, reset: time.Second, wait: true, cancel: true},
		{name: "bounded recheck", maxWait: time.Second, reset: time.Second, wait: true, twice: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testOutboundLimit(t, tc.maxWait, tc.reset, tc.wait, tc.cancel, tc.twice)
		})
	}
}

func testOutboundLimit(t *testing.T, maxWait, reset time.Duration, waits, canceled, twice bool) {
	t.Helper()
	clk := clockwork.NewFakeClockAt(time.Unix(100, 0))
	var checks atomic.Int32
	store := &scriptedStore{get: func(context.Context, string, limiter.Rate) (limiter.Context, error) {
		n := checks.Add(1)
		return limiter.Context{Reached: n == 1 || twice, Reset: clk.Now().Add(reset).Unix() - 1}, nil
	}}
	out, err := NewOutbound(TransportOptions{Rate: "1-S", Store: store, MaxWait: maxWait}, clk)
	require.NoError(t, err)
	var calls atomic.Int32
	next := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{Body: http.NoBody}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		resp, callErr := out.Wrap(next).RoundTrip(req)
		if resp != nil {
			callErr = errors.Join(callErr, resp.Body.Close())
		}
		done <- callErr
	}()
	if waits {
		deadline, stop := context.WithTimeout(t.Context(), time.Second)
		defer stop()
		require.NoError(t, clk.BlockUntilContext(deadline, 1))
		if canceled {
			cancel()
		} else {
			clk.Advance(reset)
		}
	}
	select {
	case result := <-done:
		switch {
		case canceled:
			require.ErrorIs(t, result, context.Canceled)
		case !waits || twice:
			require.ErrorIs(t, result, ErrLimited)
		default:
			require.NoError(t, result)
			require.EqualValues(t, 1, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("outbound did not finish")
	}
	require.LessOrEqual(t, checks.Load(), int32(2))
}

func TestOutboundValidation(t *testing.T) {
	t.Parallel()
	var typedNil *scriptedStore
	for _, opts := range []TransportOptions{
		{Rate: "invalid"},
		{MaxWait: -1},
		{Hosts: []HostRate{{Host: ""}}},
		{Hosts: []HostRate{{Host: "x", Rate: "bad"}}},
		{Hosts: []HostRate{{Host: "x"}, {Host: "X"}}},
		{Store: typedNil},
	} {
		_, err := NewOutbound(opts, clock.NewFake())
		require.Error(t, err)
	}
	_, err := NewOutbound(TransportOptions{}, nil)
	require.Error(t, err)
	var nilClock *clockwork.FakeClock
	_, err = NewOutbound(TransportOptions{}, nilClock)
	require.Error(t, err)
	out, err := NewOutbound(TransportOptions{}, clock.NewFake())
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://x", nil)
	require.NoError(t, err)
	require.Error(t, httpclient.RoundTripError(t, out.Wrap(nil), req))
}

func TestOutboundPassthroughAndErrors(t *testing.T) {
	t.Parallel()
	cause := errors.New("upstream failed")
	for _, rate := range []string{"", "1-S"} {
		store := &scriptedStore{get: func(context.Context, string, limiter.Rate) (limiter.Context, error) { return limiter.Context{}, nil }}
		out, err := NewOutbound(TransportOptions{Rate: rate, Store: store}, clock.NewFake())
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://x", nil)
		require.NoError(t, err)
		require.ErrorIs(t, httpclient.RoundTripError(t, out.Wrap(roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })), req), cause)
	}
	store := &scriptedStore{get: func(context.Context, string, limiter.Rate) (limiter.Context, error) { return limiter.Context{}, cause }}
	out, err := NewOutbound(TransportOptions{Rate: "1-S", Store: store}, clock.NewFake())
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://x", nil)
	require.NoError(t, err)
	upstream := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("upstream called") })
	require.ErrorIs(t, httpclient.RoundTripError(t, out.Wrap(upstream), req), cause)
}

func TestOutboundModule(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "outbound.yaml")
	require.NoError(t, os.WriteFile(path, []byte("http:\n  outbound:\n    ratelimit:\n      rate: 5-S\n      max_wait: 1s\n      hosts:\n        - host: provider.test\n          rate: 2-S\n"), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	var out *Outbound
	app := fxtest.New(t, fx.Supply(cfg), fx.Provide(func() clock.Clock { return clock.NewFake() }), OutboundModule, fx.Populate(&out), fx.NopLogger)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.Equal(t, time.Second, out.maxWait)
	require.Contains(t, out.hosts, "provider.test")
	require.NotNil(t, out.fallback)
}

func TestOutboundDefaultAndUnlimitedHost(t *testing.T) {
	t.Parallel()
	var checks atomic.Int32
	store := &scriptedStore{get: func(_ context.Context, key string, rate limiter.Rate) (limiter.Context, error) {
		checks.Add(1)
		require.Equal(t, "outbound:unlisted.test", key)
		require.EqualValues(t, 5, rate.Limit)
		return limiter.Context{}, nil
	}}
	out, err := NewOutbound(TransportOptions{Rate: "5-S", Hosts: []HostRate{{Host: "unlimited.test"}}, Store: store}, clock.NewFake())
	require.NoError(t, err)
	next := roundTripFunc(func(*http.Request) (*http.Response, error) { return &http.Response{Body: http.NoBody}, nil })
	for _, host := range []string{"unlisted.test", "UNLIMITED.test:443"} {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+"/", nil)
		require.NoError(t, reqErr)
		resp, callErr := out.Wrap(next).RoundTrip(req)
		require.NoError(t, callErr)
		require.NoError(t, resp.Body.Close())
	}
	require.EqualValues(t, 1, checks.Load())
}

func TestOutboundHostNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"api.example.com", true},
		{"API.Example.com", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"api.example.com:443", false},
		{"[::1]", false},
		{"http://x", false},
		{"a/b", false},
		{" a", false},
		{"", false},
	} {
		_, err := NewOutbound(TransportOptions{Hosts: []HostRate{{Host: tc.name}}}, clock.NewFake())
		require.Equal(t, tc.valid, err == nil, tc.name)
	}
}

type trackedBody struct {
	io.ReadCloser
	closed   bool
	closeErr error
}

func (b *trackedBody) Close() error { b.closed = true; return b.closeErr }

func TestOutboundRequestBodyOwnership(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"limited", "cancel", "nil", "success", "close error"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) { testRequestBodyOwnership(t, mode) })
		})
	}
}

func testRequestBodyOwnership(t *testing.T, mode string) {
	t.Helper()
	clk := clock.NewFake()
	store := &scriptedStore{get: func(context.Context, string, limiter.Rate) (limiter.Context, error) {
		return limiter.Context{Reached: mode != "success", Reset: clk.Now().Add(time.Second).Unix()}, nil
	}}
	opts := TransportOptions{Rate: "1-S", Store: store}
	if mode == "cancel" {
		opts.MaxWait = 3 * time.Second
	}
	out, err := NewOutbound(opts, clk)
	require.NoError(t, err)
	body := &trackedBody{ReadCloser: http.NoBody}
	closeErr := errors.New("synthetic close failure")
	if mode == "close error" {
		body.closeErr = closeErr
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider.test/", body)
	require.NoError(t, err)
	var next http.RoundTripper = roundTripFunc(func(*http.Request) (*http.Response, error) {
		require.False(t, body.closed)
		return &http.Response{Body: http.NoBody}, nil
	})
	if mode == "nil" {
		next = nil
	}
	done := make(chan error, 1)
	go func() {
		resp, callErr := out.Wrap(next).RoundTrip(req)
		if resp != nil {
			callErr = errors.Join(callErr, resp.Body.Close())
		}
		done <- callErr
	}()
	synctest.Wait()
	if mode == "cancel" {
		cancel()
	}
	requireBodyOwnership(t, mode, body, <-done, closeErr)
}

func requireBodyOwnership(t *testing.T, mode string, body *trackedBody, err, closeErr error) {
	t.Helper()
	switch mode {
	case "success":
		require.NoError(t, err)
	case "cancel":
		require.ErrorIs(t, err, context.Canceled)
	case "nil":
		require.Error(t, err)
	default:
		require.ErrorIs(t, err, ErrLimited)
	}
	if mode == "close error" {
		require.ErrorIs(t, err, closeErr)
	}
	require.Equal(t, mode != "success", body.closed)
}

func TestOutboundQuotaResetPrecision(t *testing.T) {
	t.Parallel()
	for _, budget := range []time.Duration{800 * time.Millisecond, 800*time.Millisecond - time.Nanosecond} {
		t.Run(budget.String(), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				clk := clockwork.NewFakeClockAt(time.Unix(100, 200000000))
				expiry := clk.Now().Add(400 * time.Millisecond)
				var checks atomic.Int32
				store := &scriptedStore{get: func(context.Context, string, limiter.Rate) (limiter.Context, error) {
					checks.Add(1)
					return limiter.Context{Reached: clk.Now().Before(expiry), Reset: expiry.Unix()}, nil
				}}
				out, err := NewOutbound(TransportOptions{Rate: "1-S", Store: store, MaxWait: budget}, clk)
				require.NoError(t, err)
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://provider.test", nil)
				require.NoError(t, err)
				next := roundTripFunc(func(*http.Request) (*http.Response, error) { return &http.Response{Body: http.NoBody}, nil })
				done := make(chan error, 1)
				go func() {
					resp, callErr := out.Wrap(next).RoundTrip(req)
					if resp != nil {
						callErr = errors.Join(callErr, resp.Body.Close())
					}
					done <- callErr
				}()
				synctest.Wait()
				if budget < 800*time.Millisecond {
					require.ErrorIs(t, <-done, ErrLimited)
					require.EqualValues(t, 1, checks.Load())
					return
				}
				select {
				case result := <-done:
					t.Errorf("returned before expiry: %v", result)
					return
				default:
				}
				clk.Advance(budget - time.Nanosecond)
				synctest.Wait()
				require.EqualValues(t, 1, checks.Load())
				clk.Advance(time.Nanosecond)
				require.NoError(t, <-done)
				require.EqualValues(t, 2, checks.Load())
			})
		})
	}
}
