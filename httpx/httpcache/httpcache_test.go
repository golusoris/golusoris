// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bartventer/httpcache"
	"github.com/bartventer/httpcache/store"
	"github.com/bartventer/httpcache/store/driver"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/twotier"
	"github.com/golusoris/golusoris/testutil/httpclient"
)

func testCache(t *testing.T, opts Options) (*Cache, *twotier.TwoTier) {
	t.Helper()
	l1, err := memory.NewForTest(1000, 0)
	require.NoError(t, err)
	tt, err := twotier.New(l1, twotier.Options{L2: twotier.L2None}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	c, err := New(opts, tt, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c, tt
}

func response(status int, body string, headers ...string) *http.Response {
	resp := &http.Response{
		StatusCode: status, Status: strconv.Itoa(status) + " " + http.StatusText(status),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)),
	}
	for i := 0; i+1 < len(headers); i += 2 {
		resp.Header.Add(headers[i], headers[i+1])
	}
	return resp
}

func request(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	return req
}

// served is a fully read and closed response, so tests never hold an open body.
type served struct {
	StatusCode int
	Header     http.Header
}

func readResponse(t *testing.T, transport http.RoundTripper, req *http.Request) (served, string) {
	t.Helper()
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	return readBody(t, resp)
}

func readBody(t *testing.T, resp *http.Response) (served, string) {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Empty(t, resp.Header.Get(syntheticHeader))
	return served{StatusCode: resp.StatusCode, Header: resp.Header}, string(data)
}

func TestCacheHitAndAgeBoundary(t *testing.T) {
	t.Parallel()
	for _, age := range []int{0, 59, 60, 61} {
		t.Run(strconv.Itoa(age), func(t *testing.T) {
			t.Parallel()
			c, _ := testCache(t, DefaultOptions())
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, "metadata", "Cache-Control", "max-age=60", "Age", strconv.Itoa(age)), nil
			}))
			readResponse(t, transport, request(t, "http://provider.test/data"))
			resp, body := readResponse(t, transport, request(t, "http://provider.test/data"))
			require.Equal(t, "metadata", body)
			if age < 60 {
				require.Equal(t, "HIT", resp.Header.Get("X-Httpcache-Status"))
				require.Equal(t, 1, calls)
			} else {
				require.NotEqual(t, "HIT", resp.Header.Get("X-Httpcache-Status"))
				require.Equal(t, 2, calls)
			}
		})
	}
}

func TestCacheETagRevalidation(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	calls := 0
	transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, "metadata", "Cache-Control", "max-age=60", "Age", "61", "ETag", `"v1"`), nil
		}
		require.Equal(t, `"v1"`, req.Header.Get("If-None-Match"))
		return response(304, "", "Cache-Control", "max-age=60"), nil
	}))
	readResponse(t, transport, request(t, "http://provider.test/data"))
	resp, body := readResponse(t, transport, request(t, "http://provider.test/data"))
	require.Equal(t, "metadata", body)
	require.Equal(t, "REVALIDATED", resp.Header.Get("X-Httpcache-Status"))
	require.Equal(t, 2, calls)
}

func TestCacheStaleOnProviderFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("provider unavailable")
	for _, status := range []int{0, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			c, _ := testCache(t, DefaultOptions())
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return response(200, "cached", "Cache-Control", "max-age=60", "Age", "61"), nil
				}
				if status == 0 {
					return nil, cause
				}
				return response(status, "busy"), nil
			}))
			readResponse(t, transport, request(t, "http://provider.test/data"))
			resp, body := readResponse(t, transport, request(t, "http://provider.test/data"))
			require.Equal(t, "cached", body)
			require.Equal(t, "STALE", resp.Header.Get("X-Httpcache-Status"))
		})
	}
}

func TestCacheOriginalFailures(t *testing.T) {
	t.Parallel()
	cause := errors.New("original failure")
	for _, prefill := range []bool{false, true} {
		t.Run(strconv.FormatBool(prefill), func(t *testing.T) {
			t.Parallel()
			c, _ := testCache(t, DefaultOptions())
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if prefill && calls == 1 {
					return response(200, "too old", "Cache-Control", "max-age=60", "Age", "3661"), nil
				}
				return nil, cause
			}))
			if prefill {
				readResponse(t, transport, request(t, "http://provider.test/data"))
			}
			require.ErrorIs(t, httpclient.RoundTripError(t, transport, request(t, "http://provider.test/data")), cause)
		})
	}
	c, _ := testCache(t, DefaultOptions())
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) { return response(429, "slow down"), nil }))
	resp, body := readResponse(t, transport, request(t, "http://provider.test/data"))
	require.Equal(t, 429, resp.StatusCode)
	require.Equal(t, "slow down", body)
}

func TestCacheSecretsAndVary(t *testing.T) {
	t.Parallel()
	c, tt := testCache(t, DefaultOptions())
	seen := make([]string, 0, 2)
	transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req.URL.Query().Get("api_key"))
		return response(200, "public metadata", "Cache-Control", "max-age=60", "Vary", "Authorization, Accept"), nil
	}))
	req := request(t, "http://provider.test/data?api_key=K1")
	req.Header.Set("Authorization", "Bearer example-private-value")
	readResponse(t, transport, req)
	req2 := request(t, "http://provider.test/data?api_key=K2")
	req2.Header.Set("Authorization", "Bearer rotated")
	resp, _ := readResponse(t, transport, req2)
	require.Equal(t, "HIT", resp.Header.Get("X-Httpcache-Status"))
	require.Equal(t, []string{"K1"}, seen)
	require.Equal(t, "K1", req.URL.Query().Get("api_key"))
	conn, err := store.Open(driverScheme + "://" + c.id)
	require.NoError(t, err)
	refs, err := conn.Get("http://provider.test/data")
	require.NoError(t, err)
	require.NotContains(t, string(refs), "K1")
	require.NotContains(t, string(refs), "K2")
	require.NotContains(t, string(refs), "Bearer")
	require.NotContains(t, string(refs), "Authorization")
	var entries []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(refs, &entries))
	require.Len(t, entries, 1)
	view := twotier.NewTyped[[]byte](tt, c.opts.Prefix)
	raw, err := view.Get(t.Context(), entries[0].ID, func(context.Context) ([]byte, error) { return nil, errMiss })
	require.NoError(t, err)
	for _, secret := range []string{"K1", "K2", "example-private-value", "rotated"} {
		require.NotContains(t, string(raw), secret)
	}
	require.Equal(t, "Accept", resp.Header.Get("Vary"))
}

func TestCacheNoStore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		status  int
		body    string
		headers []string
	}{
		{"401", 401, "denied", nil},
		{"403", 403, "denied", nil},
		{"407", 407, "denied", nil},
		{"cookie", 200, "ok", []string{"Set-Cookie", "example=value"}},
		{"www", 200, "ok", []string{"WWW-Authenticate", "Bearer"}},
		{"proxy", 200, "ok", []string{"Proxy-Authenticate", "Basic"}},
		{"authinfo", 200, "ok", []string{"Authentication-Info", "value"}},
		{"secret header", 200, "ok", []string{"X-Api-Key", "example"}},
		{"json", 200, `{"access_token": "example"}`, nil},
		{"xml", 200, `<token>example</token>`, nil},
		{"form", 200, "token=example", nil},
		{"oversized", 200, strings.Repeat("x", 65), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := DefaultOptions()
			opts.MaxBodyBytes = 64
			c, _ := testCache(t, opts)
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				headers := append([]string{"Cache-Control", "max-age=60"}, tc.headers...)
				return response(tc.status, tc.body, headers...), nil
			}))
			for range 2 {
				_, body := readResponse(t, transport, request(t, "http://provider.test/data"))
				require.Equal(t, tc.body, body)
			}
			require.Equal(t, 2, calls)
			conn, err := store.Open(driverScheme + "://" + c.id)
			require.NoError(t, err)
			_, err = conn.Get("http://provider.test/data")
			require.ErrorIs(t, err, driver.ErrNotExist)
		})
	}
}

func TestCacheAuthPaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/v4/login", "/oauth/token", "/pin", "/LOGIN", "/logins", "/pinned"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			c, _ := testCache(t, DefaultOptions())
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, "ok", "Cache-Control", "max-age=60"), nil
			}))
			readResponse(t, transport, request(t, "http://provider.test"+path))
			resp, _ := readResponse(t, transport, request(t, "http://provider.test"+path))
			if path == "/logins" || path == "/pinned" {
				require.Equal(t, 1, calls)
				require.Equal(t, "HIT", resp.Header.Get("X-Httpcache-Status"))
			} else {
				require.Equal(t, 2, calls)
				require.Empty(t, resp.Header.Get("X-Httpcache-Status"))
			}
		})
	}
}

func TestCacheHostTTL(t *testing.T) {
	t.Parallel()
	for _, directive := range []string{"no-cache", "no-store"} {
		t.Run(directive, func(t *testing.T) {
			t.Parallel()
			opts := DefaultOptions()
			opts.Hosts = []HostOptions{{Host: "PROVIDER.test", TTL: time.Minute}}
			c, _ := testCache(t, opts)
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, "ok", "Cache-Control", directive), nil
			}))
			readResponse(t, transport, request(t, "http://provider.test:80/data"))
			resp, _ := readResponse(t, transport, request(t, "http://provider.test:80/data"))
			if directive == "no-cache" {
				require.Equal(t, 1, calls)
				require.Equal(t, "HIT", resp.Header.Get("X-Httpcache-Status"))
			} else {
				require.Equal(t, 2, calls)
			}
		})
	}
}

func TestCacheSingleflight(t *testing.T) {
	t.Parallel()
	for _, oversized := range []bool{false, true} {
		t.Run(strconv.FormatBool(oversized), func(t *testing.T) { t.Parallel(); testConcurrentMisses(t, oversized) })
	}
}

func testConcurrentMisses(t *testing.T, oversized bool) {
	t.Helper()
	opts := DefaultOptions()
	opts.MaxBodyBytes = 64
	c, _ := testCache(t, opts)
	const count = 16
	var calls atomic.Int32
	started := make(chan struct{}, count)
	gate := make(chan struct{})
	payload := "ok"
	if oversized {
		payload = strings.Repeat("x", 65)
	}
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		started <- struct{}{}
		<-gate
		return response(200, payload, "Cache-Control", "max-age=60"), nil
	}))
	var workers sync.WaitGroup
	ready := make(chan struct{}, count)
	results := make(chan string, count)
	for range count {
		workers.Go(func() {
			ready <- struct{}{}
			_, body := readResponse(t, transport, request(t, "http://provider.test/data"))
			results <- body
		})
	}
	for range count {
		<-ready
	}
	<-started
	// Give all callers a chance to join the blocked upstream flight.
	time.Sleep(30 * time.Millisecond)
	close(gate)
	workers.Wait()
	for range count {
		require.Equal(t, payload, <-results)
	}
	if oversized {
		require.GreaterOrEqual(t, calls.Load(), int32(1))
	} else {
		require.EqualValues(t, 1, calls.Load())
	}
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
	req := request(t, "http://provider.test/two").WithContext(ctx)
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
	second := request(t, "http://provider.test/two").WithContext(ctx)
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
		require.Equal(t, "value", req.URL.Query().Get("public"))
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

func TestCacheSWRRestoresRotatedSecret(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.StaleWhileRevalidate = time.Minute
	c, _ := testCache(t, opts)
	observed := make(chan string, 2)
	var calls atomic.Int32
	transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		observed <- req.URL.Query().Get("api_key")
		if calls.Add(1) == 1 {
			return response(200, "cached", "Cache-Control", "max-age=60", "Age", "61"), nil
		}
		return response(200, "updated", "Cache-Control", "max-age=60"), nil
	}))
	readResponse(t, transport, request(t, "http://provider.test/data?api_key=K1"))
	require.Equal(t, "K1", <-observed)
	resp, body := readResponse(t, transport, request(t, "http://provider.test/data?api_key=K2"))
	require.Equal(t, "cached", body)
	require.Equal(t, "STALE", resp.Header.Get("X-Httpcache-Status"))
	select {
	case value := <-observed:
		require.Equal(t, "K2", value)
	case <-time.After(time.Second):
		t.Fatal("background revalidation missing")
	}
}

func TestCacheSWRSurvivesCallerClose(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.StaleWhileRevalidate = time.Minute
	c, _ := testCache(t, opts)
	foreground := make(chan context.Context, 1)
	closed := make(chan struct{})
	background := make(chan error, 1)
	var calls atomic.Int32
	client := &http.Client{Timeout: time.Minute, Transport: c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		switch calls.Add(1) {
		case 1:
			foreground <- req.Context()
			return response(200, "cached", "Cache-Control", "max-age=60", "Age", "61"), nil
		case 2:
			<-closed
			background <- req.Context().Err()
		}
		return response(200, "updated", "Cache-Control", "max-age=60"), nil
	}))}
	get := func() (served, string) {
		resp, err := client.Do(request(t, "http://provider.test/data"))
		require.NoError(t, err)
		return readBody(t, resp)
	}
	_, body := get()
	require.Equal(t, "cached", body)
	require.Error(t, (<-foreground).Err(), "foreground upstream work must end with the caller")
	resp, body := get()
	require.Equal(t, "cached", body)
	require.Equal(t, "STALE", resp.Header.Get(httpcache.CacheStatusHeader))
	close(closed)
	select {
	case err := <-background:
		require.NoError(t, err, "closing a stale body must not cancel revalidation")
	case <-time.After(5 * time.Second):
		t.Fatal("background revalidation missing")
	}
	for range 100 {
		if resp, body = get(); body == "updated" {
			break
		}
		<-time.After(10 * time.Millisecond)
	}
	require.Equal(t, "updated", body)
	require.Equal(t, "HIT", resp.Header.Get(httpcache.CacheStatusHeader))
}

func TestCacheSensitive304NeverStored(t *testing.T) {
	t.Parallel()
	for _, conditional := range []bool{false, true} {
		t.Run("conditional="+strconv.FormatBool(conditional), func(t *testing.T) {
			t.Parallel()
			c, _ := testCache(t, DefaultOptions())
			calls := 0
			transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				switch calls {
				case 1:
					return response(200, "cached", "Cache-Control", "max-age=60", "Age", "61", "ETag", `"v1"`), nil
				case 2:
					require.Equal(t, `"v1"`, req.Header.Get("If-None-Match"))
					return response(304, "", "Set-Cookie", "private-example-cookie"), nil
				default:
					require.Empty(t, req.Header.Get("If-None-Match"))
					return response(200, "fresh", "Set-Cookie", "private-example-cookie"), nil
				}
			}))
			readResponse(t, transport, request(t, "http://provider.test/data"))
			req := request(t, "http://provider.test/data")
			status, expected, upstreamCalls := http.StatusOK, "fresh", 3
			if conditional {
				req.Header.Set("If-None-Match", `"v1"`)
				status, expected, upstreamCalls = http.StatusNotModified, "", 2
			}
			resp, body := readResponse(t, transport, req)
			require.Equal(t, status, resp.StatusCode)
			require.Equal(t, expected, body)
			require.Equal(t, "private-example-cookie", resp.Header.Get("Set-Cookie"))
			require.Equal(t, upstreamCalls, calls)
			requireStoredWithout(t, c, "http://provider.test/data", "private-example-cookie")
		})
	}
}

func requireStoredWithout(t *testing.T, c *Cache, target, secret string) {
	t.Helper()
	conn, err := store.Open(driverScheme + "://" + c.id)
	require.NoError(t, err)
	rawRefs, err := conn.Get(target)
	require.NoError(t, err)
	var refs []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rawRefs, &refs))
	require.Len(t, refs, 1)
	raw, err := conn.Get(refs[0].ID)
	require.NoError(t, err)
	require.NotContains(t, string(raw), secret)
}

func TestCacheBodySizeBoundary(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 64, 65} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			opts := DefaultOptions()
			opts.MaxBodyBytes = 64
			c, _ := testCache(t, opts)
			calls := 0
			body := strings.Repeat("x", size)
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, body, "Cache-Control", "max-age=60"), nil
			}))
			for range 2 {
				_, actual := readResponse(t, transport, request(t, "http://provider.test/data"))
				require.Equal(t, body, actual)
			}
			expected := 1
			if size > 64 {
				expected = 2
			}
			require.Equal(t, expected, calls)
		})
	}
}
