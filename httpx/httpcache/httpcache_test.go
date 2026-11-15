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
	"testing"
	"time"

	"github.com/bartventer/httpcache/store"
	"github.com/bartventer/httpcache/store/driver"
	"github.com/stretchr/testify/assert"
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
		assert.Equal(t, `"v1"`, req.Header.Get("If-None-Match"))
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

func TestCacheAnonymousSecretVaryNotStored(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.Hosts = []HostOptions{{Host: "provider.test"}}
	c, _ := testCache(t, opts)
	calls := 0
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, "metadata", "Cache-Control", "max-age=60", "Vary", "Authorization, Accept"), nil
	}))
	readResponse(t, transport, request(t, "http://provider.test/data"))
	req := request(t, "http://provider.test/data")
	req.Header.Set("Authorization", "Bearer synthetic")
	readResponse(t, transport, req)
	require.Equal(t, 2, calls)
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
		{"xml namespaced", 200, `<r xmlns:a="urn:auth"><a:access_token>S</a:access_token></r>`, nil},
		{"form", 200, "token=example", nil},
		{"oversized", 200, strings.Repeat("x", 65), nil},
		{"json escaped untyped", 200, `{"access\u005ftoken":"S"}`, nil},
		{"json escaped text", 200, `{"access\u005ftoken":"S"}`, []string{"Content-Type", "text/plain"}},
		{"xml unicode prefix", 200, `<é:token>S</é:token>`, nil},
		{"form partial text", 200, "access%5Ftoken=S&other=%zz", []string{"Content-Type", "text/plain"}},
		{"json malformed declared", 200, `{"name":`, []string{"Content-Type", "application/json"}},
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
