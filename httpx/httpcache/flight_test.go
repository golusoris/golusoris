// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/testutil/httpclient"
)

func TestCacheSingleflight(t *testing.T) {
	t.Parallel()
	for _, oversized := range []bool{false, true} {
		t.Run(strconv.FormatBool(oversized), func(t *testing.T) { t.Parallel(); testConcurrentMisses(t, oversized) })
	}
}

func testConcurrentMisses(t *testing.T, oversized bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		opts := DefaultOptions()
		opts.MaxBodyBytes = 64
		c, _ := testCache(t, opts)
		const count = 16
		var calls atomic.Int32
		gate := make(chan struct{})
		payload := "ok"
		if oversized {
			payload = strings.Repeat("x", 65)
		}
		transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			<-gate
			return response(200, payload, "Cache-Control", "max-age=60"), nil
		}))
		type missResult struct{ body, status string }
		results := make(chan missResult, count)
		for range count {
			go func() {
				resp, body := readResponse(t, transport, request(t, "http://provider.test/data"))
				results <- missResult{body: body, status: resp.Header.Get("X-Httpcache-Status")}
			}()
		}
		synctest.Wait()
		close(gate)
		for range count {
			result := <-results
			require.Equal(t, payload, result.body)
			require.Equal(t, "MISS", result.status)
		}
		expected := int32(1)
		if oversized {
			expected = count
		}
		require.Equal(t, expected, calls.Load())
	})
}

func TestCacheSemaphoreCancellation(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.MaxInFlight = 1
	c, _ := testCache(t, opts)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-release
		return response(200, "ok"), nil
	}))
	done := make(chan struct{}, 1)
	go func() { readResponse(t, transport, request(t, "http://provider.test/one")); done <- struct{}{} }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	req := requestWith(t, ctx, "http://provider.test/two")
	cancel()
	require.ErrorIs(t, httpclient.RoundTripError(t, transport, req), context.Canceled)
	close(release)
	<-done
	require.Empty(t, started)
}

func TestCacheDistinctFlightsAreBounded(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.MaxInFlight = 1
	c, _ := testCache(t, opts)
	var active, peak atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		now := active.Add(1)
		peak.Store(max(peak.Load(), now))
		entered <- struct{}{}
		select {
		case <-release:
		case <-req.Context().Done():
		}
		active.Add(-1)
		return response(200, "ok"), nil
	}))
	var workers sync.WaitGroup
	for _, path := range []string{"/one", "/two"} {
		workers.Go(func() { readResponse(t, transport, request(t, "http://provider.test"+path)) })
	}
	<-entered
	require.EqualValues(t, 1, active.Load())
	close(release)
	workers.Wait()
	require.EqualValues(t, 1, peak.Load())
	require.EqualValues(t, 0, active.Load())
}

func TestCacheOversizedHoldsSlotUntilClose(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.MaxInFlight = 1
	opts.MaxBodyBytes = 2
	c, _ := testCache(t, opts)
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) { return response(200, "large body"), nil }))
	resp, err := transport.RoundTrip(request(t, "http://provider.test/one"))
	require.NoError(t, err)
	require.Len(t, c.slots, 1)
	ctx, cancel := context.WithCancel(t.Context())
	second := requestWith(t, ctx, "http://provider.test/two")
	done := make(chan error, 1)
	go func() { done <- httpclient.RoundTripError(t, transport, second) }()
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "large body", string(data))
	require.NoError(t, resp.Body.Close())
	require.NoError(t, resp.Body.Close())
	require.Empty(t, c.slots)
}

func TestCacheFailureHandoffAfterReturn(t *testing.T) {
	t.Parallel()
	state := &requestState{}
	state.failure.Store(completedFailure)
	closed := false
	resp := response(429, "busy")
	resp.Body = &streamBody{Reader: strings.NewReader("busy"), original: resp.Body, release: func() { closed = true }}
	require.NoError(t, recordFailure(state, &upstreamFailure{response: resp}))
	require.True(t, closed)
	require.NoError(t, resp.Body.Close(), "second close is a no-op")
	require.Same(t, completedFailure, state.failure.Load())
}

func TestCacheUncachedSecretsReachProvider(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	seen := make([]string, 0, 2)
	transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req.URL.Query().Get("API_KEY"))
		assert.Equal(t, "value", req.URL.Query().Get("public"))
		return response(200, "ok", "Cache-Control", "no-store"), nil
	}))
	for _, value := range []string{"K1", "K2"} {
		readResponse(t, transport, request(t, "http://provider.test/data?API_KEY="+value+"&public=value"))
	}
	require.Equal(t, []string{"K1", "K2"}, seen)
}

func TestCacheOversizedFailureReleasesSlot(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.MaxInFlight = 1
	opts.MaxBodyBytes = 8
	c, _ := testCache(t, opts)
	calls := 0
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, "cached", "Cache-Control", "max-age=60", "Age", "61"), nil
		}
		return response(503, "oversized provider error", "Cache-Control", "no-store"), nil
	}))
	readResponse(t, transport, request(t, "http://provider.test/data"))
	_, body := readResponse(t, transport, request(t, "http://provider.test/data"))
	require.Equal(t, "cached", body)
	require.Empty(t, c.slots)
}

func TestCacheSingleflightRespectsVary(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c, _ := testCache(t, DefaultOptions())
		gate := make(chan struct{})
		transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
			<-gate
			return response(200, req.Header.Get("X-Variant"), "Cache-Control", "max-age=60", "Vary", "X-Variant"), nil
		}))
		results := make(chan string, 2)
		for _, variant := range []string{"a", "b"} {
			go func() {
				req := request(t, "http://provider.test/data")
				req.Header.Set("X-Variant", variant)
				_, body := readResponse(t, transport, req)
				results <- variant + ":" + body
			}()
		}
		synctest.Wait()
		close(gate)
		require.ElementsMatch(t, []string{"a:a", "b:b"}, []string{<-results, <-results})
	})
}

func TestCacheLeaderCancelDoesNotFailFollowers(t *testing.T) {
	t.Parallel()
	testFlightCancellation(t, true)
}

func TestCacheFollowerCancelReturnsPromptly(t *testing.T) {
	t.Parallel()
	testFlightCancellation(t, false)
}

func testFlightCancellation(t *testing.T, cancelLeader bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		c, _ := testCache(t, DefaultOptions())
		gate := make(chan struct{})
		var calls atomic.Int32
		transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				select {
				case <-gate:
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}
			return response(200, "metadata", "Cache-Control", "max-age=60"), nil
		}))
		leaderCtx, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		followerCtx, cancelSecond := context.WithCancel(t.Context())
		defer cancelSecond()
		leader, follower := make(chan error, 1), make(chan error, 1)
		send := func(ctx context.Context, done chan error) {
			flightResultTo(t, transport, requestWith(t, ctx, "http://provider.test/data"), done)
		}
		go send(leaderCtx, leader)
		synctest.Wait()
		go send(followerCtx, follower)
		synctest.Wait()
		if cancelLeader {
			cancelFirst()
		} else {
			cancelSecond()
		}
		synctest.Wait()
		select {
		case err := <-follower:
			requireFlightError(t, err, !cancelLeader)
		default:
			t.Error("follower still blocked after cancellation")
		}
		close(gate)
		requireFlightError(t, <-leader, cancelLeader)
		expected := int32(1)
		if cancelLeader {
			expected = 2
		}
		require.Equal(t, expected, calls.Load())
	})
}

func TestCacheConditionalFlightKeys(t *testing.T) {
	t.Parallel()
	req := request(t, "http://provider.test/data")
	key := func(conditional bool) flightKey {
		state := &requestState{conditional: conditional}
		return upstreamKey(req.WithContext(context.WithValue(req.Context(), requestStateKey{}, state)))
	}
	require.NotEqual(t, key(true), key(false))
}

func flightResultTo(t *testing.T, transport http.RoundTripper, req *http.Request, done chan error) {
	t.Helper()
	resp, err := transport.RoundTrip(req)
	if resp != nil {
		_, err = io.Copy(io.Discard, resp.Body)
		err = errors.Join(err, resp.Body.Close())
	}
	done <- err
}

func requireFlightError(t *testing.T, err error, canceled bool) {
	t.Helper()
	if canceled {
		require.ErrorIs(t, err, context.Canceled)
	} else {
		require.NoError(t, err)
	}
}

type countedBody struct {
	io.Reader
	closes atomic.Int32
	err    error
}

func (b *countedBody) Close() error {
	b.closes.Add(1)
	return b.err
}

func TestCacheCanceledLeaderReleasesStreamingSlot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		opts := DefaultOptions()
		opts.MaxInFlight = 2
		c, _ := testCache(t, opts)
		gate := make(chan struct{})
		body := &countedBody{Reader: strings.NewReader("stream")}
		var calls atomic.Int32
		transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
			resp := response(200, "stream", "Cache-Control", "no-store")
			if calls.Add(1) == 1 {
				<-gate
				resp.Body = body
			}
			return resp, nil
		}))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go flightResultTo(t, transport, requestWith(t, ctx, "http://provider.test/data"), done)
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
		close(gate)
		synctest.Wait()
		require.EqualValues(t, 1, body.closes.Load())
		require.Empty(t, c.slots)
		for range opts.MaxInFlight + 1 {
			bounded, stop := context.WithTimeout(t.Context(), time.Second)
			_, data := readResponse(t, transport, requestWith(t, bounded, "http://provider.test/data"))
			stop()
			require.Equal(t, "stream", data)
		}
		require.Empty(t, c.slots)
		require.EqualValues(t, opts.MaxInFlight+2, calls.Load())
	})
}
