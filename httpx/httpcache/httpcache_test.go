// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	t.Cleanup(func() { require.True(t, l1.StopAllGoroutines()) })
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
	return requestWith(t, t.Context(), target)
}

func requestWith(t *testing.T, ctx context.Context, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
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
	opts := DefaultOptions()
	opts.Hosts = []HostOptions{{Host: "provider.test", Shared: true}}
	c, tt := testCache(t, opts)
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
	opts.Hosts = []HostOptions{{Host: "provider.test", Shared: true}}
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

func TestCacheCredentialedResponsesNeedSharing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, control, vary          string
		credentialed, stored, shared bool
	}{
		{"private", "max-age=60", "", true, false, false},
		{"public", "public, max-age=60", "", true, true, false},
		{"shared age", "s-maxage=60, max-age=60", "", true, true, false},
		{"revalidate", "must-revalidate, max-age=60", "", true, true, false},
		{"secret vary", "public, max-age=60", "Authorization", true, false, false},
		{"shared host", "max-age=60", "", true, true, true},
		{"anonymous", "max-age=60", "", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := DefaultOptions()
			opts.Hosts = []HostOptions{{Host: "provider.test", Shared: tc.shared}}
			c, _ := testCache(t, opts)
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, "metadata", "Cache-Control", tc.control, "Vary", tc.vary), nil
			}))
			for range 2 {
				req := request(t, "http://provider.test/data")
				if tc.credentialed {
					req.Header.Set("Authorization", "Bearer synthetic")
				}
				readResponse(t, transport, req)
			}
			expected := 2
			if tc.stored {
				expected = 1
			}
			require.Equal(t, expected, calls)
		})
	}
}

func TestCacheUserinfoNeverLoggedOrStored(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "synthetic-user", req.URL.User.Username())
		password, ok := req.URL.User.Password()
		require.True(t, ok)
		require.Equal(t, "synthetic-password", password)
		return response(200, "metadata", "Cache-Control", "public, max-age=60"), nil
	}))
	readResponse(t, transport, request(t, "http://synthetic-user:synthetic-password@provider.test/data"))
	require.NotContains(t, logs.String(), "synthetic-password")
	require.NotContains(t, logs.String(), "synthetic-user")
	requireStoredWithout(t, c, "http://provider.test/data", "synthetic-password")
}

func TestCacheRawQueryPreserved(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"b=2&a=%7e&flag&api_key=S", "b=2&a=%7e&flag&api_key=SECRETVALUE", "b=2&x=1;api_key=SECRETVALUE&a=%7e&flag"} {
		c, _ := testCache(t, DefaultOptions())
		transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, query, req.URL.RawQuery)
			return response(200, "metadata", "Cache-Control", "public, max-age=60"), nil
		}))
		req := request(t, "http://provider.test/data?"+query)
		readResponse(t, transport, req)
		stripped := req.Clone(req.Context())
		c.stripSecrets(stripped, &requestState{})
		require.Equal(t, "b=2&a=%7e&flag", stripped.URL.RawQuery)
		requireStoredWithout(t, c, "http://provider.test/data?b=2&a=~&flag", "SECRETVALUE")
	}
}

func TestCacheNonStorableStreamsUnbuffered(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		opts := DefaultOptions()
		opts.MaxBodyBytes, opts.MaxInFlight = 8, 1
		c, _ := testCache(t, opts)
		reader, writer := io.Pipe()
		gate := make(chan struct{})
		var calls atomic.Int32
		transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) > 1 {
				return response(200, "second"), nil
			}
			go func() {
				_, err := writer.Write([]byte("first"))
				if err == nil {
					<-gate
					_, err = writer.Write([]byte("remaining body"))
				}
				require.NoError(t, writer.CloseWithError(err))
			}()
			resp := response(200, "", "Cache-Control", "no-store")
			resp.Body, resp.ContentLength = reader, -1
			return resp, nil
		}))
		done := make(chan *http.Response, 1)
		go func() {
			resp, err := transport.RoundTrip(request(t, "http://provider.test/first")) //nolint:bodyclose // WHY: the receiving select reads and closes the streamed body after the hand-off.
			require.NoError(t, err)
			done <- resp
		}()
		synctest.Wait()
		select {
		case resp := <-done:
			first := make([]byte, 5)
			_, err := io.ReadFull(resp.Body, first)
			require.NoError(t, err)
			require.Equal(t, "first", string(first))
			require.Len(t, c.slots, 1)
			require.NoError(t, resp.Body.Close())
			require.Empty(t, c.slots)
			readResponse(t, transport, request(t, "http://provider.test/second"))
			close(gate)
		default:
			t.Error("no-store response buffered before caller could read first chunk")
			close(gate)
			resp := <-done
			require.NoError(t, resp.Body.Close())
		}
	})
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

func TestCacheBackgroundRevalidationBounded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		opts := DefaultOptions()
		opts.MaxInFlight = 1
		opts.StaleWhileRevalidate = time.Minute
		c, _ := testCache(t, opts)
		gate := make(chan struct{})
		var calls atomic.Int32
		transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) <= 2 {
				return response(200, "cached", "Cache-Control", "max-age=60", "Age", "61"), nil
			}
			<-gate
			return response(200, "updated", "Cache-Control", "max-age=60"), nil
		}))
		for _, path := range []string{"/one", "/two"} {
			readResponse(t, transport, request(t, "http://provider.test"+path))
		}
		for _, path := range []string{"/one", "/two"} {
			resp, body := readResponse(t, transport, request(t, "http://provider.test"+path))
			require.Equal(t, "STALE", resp.Header.Get("X-Httpcache-Status"))
			require.Equal(t, "cached", body)
			synctest.Wait()
		}
		require.EqualValues(t, 3, calls.Load())
		close(gate)
		synctest.Wait()
		require.EqualValues(t, 3, calls.Load(), "second background request must be rejected, not queued")
	})
}

func TestCacheSensitive304SharedRefetch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c, _ := testCache(t, DefaultOptions())
		gate := make(chan struct{})
		var warm atomic.Bool
		var unconditional atomic.Int32
		transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
			if !warm.Swap(true) {
				return response(200, "cached", "Cache-Control", "max-age=60", "Age", "61", "ETag", `"v1"`), nil
			}
			if req.Header.Get("If-None-Match") != "" {
				<-gate
				return response(304, "", "Cache-Control", "no-store"), nil
			}
			unconditional.Add(1)
			return response(200, "fresh", "Cache-Control", "max-age=60"), nil
		}))
		readResponse(t, transport, request(t, "http://provider.test/data"))
		const count = 8
		results := make(chan string, count)
		for range count {
			go func() {
				_, body := readResponse(t, transport, request(t, "http://provider.test/data"))
				results <- body
			}()
		}
		synctest.Wait()
		close(gate)
		for range count {
			require.Equal(t, "fresh", <-results)
		}
		require.EqualValues(t, 1, unconditional.Load())
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

func TestCacheOversizedTrailers(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Metadata-Version")
		w.Header().Set("Cache-Control", "max-age=60")
		_, err := io.WriteString(w, strings.Repeat("x", 128))
		require.NoError(t, err)
		w.Header().Set("X-Metadata-Version", "v2")
	}))
	defer server.Close()
	opts := DefaultOptions()
	opts.MaxBodyBytes = 8
	c, _ := testCache(t, opts)
	upstream := &http.Transport{}
	defer upstream.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	resp, err := c.Wrap(upstream).RoundTrip(requestWith(t, ctx, server.URL))
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, data, 128)
	require.Equal(t, "v2", resp.Trailer.Get("X-Metadata-Version"))
	require.NoError(t, resp.Body.Close())
}

func TestCacheUpstreamErrorOmitsSecrets(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	c, _ := testCache(t, DefaultOptions())
	upstream := &http.Transport{}
	defer upstream.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req := requestWith(t, ctx, "http://synthetic-user:SECRETPASSWORD@"+address+"/?api_key=SECRETVALUE")
	resp, err := c.Wrap(upstream).RoundTrip(req)
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}
	require.Nil(t, resp)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRETVALUE")
	require.NotContains(t, err.Error(), "SECRETPASSWORD")
	var operation *net.OpError
	require.ErrorAs(t, err, &operation)
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
