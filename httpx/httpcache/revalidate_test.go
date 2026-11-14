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
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bartventer/httpcache"
	"github.com/bartventer/httpcache/store"
	"github.com/stretchr/testify/require"
)

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
		{"invalid shared age", "max-age=60, s-maxage=invalid", "", true, false, false},
		{"empty shared age", "max-age=60, s-maxage=", "", true, false, false},
		{"negative shared age", "max-age=60, s-maxage=-1", "", true, false, false},
		{"quoted shared age", `max-age=60, S-Maxage="60"`, "", true, true, false},
		{"zero shared age", "max-age=60, s-maxage=0", "", true, false, false},
		{"missing shared age", "max-age=60, s-maxage", "", true, false, false},
		{"empty quoted shared age", `max-age=60, s-maxage=""`, "", true, false, false},
		{"signed shared age", "max-age=60, s-maxage=+60", "", true, false, false},
		{"fractional shared age", "max-age=60, s-maxage=6.0", "", true, false, false},
		{"spaced shared age", `max-age=60, s-maxage=" 60"`, "", true, false, false},
		{"unicode shared age", "max-age=60, s-maxage=６０", "", true, false, false},
		{"unclosed shared age", `max-age=60, s-maxage="60`, "", true, false, false},
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
		writes := make(chan error, 1)
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
				writes <- writer.CloseWithError(err)
			}()
			resp := response(200, "", "Cache-Control", "no-store")
			resp.Body, resp.ContentLength = reader, -1
			return resp, nil
		}))
		done := make(chan roundTripResult, 1)
		req := request(t, "http://provider.test/first")
		go func() {
			resp, err := transport.RoundTrip(req) //nolint:bodyclose // WHY: the receiving select reads and closes the streamed body after the hand-off.
			done <- roundTripResult{resp: resp, err: err}
		}()
		synctest.Wait()
		select {
		case result := <-done:
			require.NoError(t, result.err)
			resp := result.resp
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
			result := <-done
			require.NoError(t, result.err)
			require.NoError(t, result.resp.Body.Close())
		}
		require.NoError(t, <-writes)
	})
}

type roundTripResult struct {
	resp *http.Response
	err  error
}

type readResult struct {
	resp served
	body string
	err  error
}

func readResponseResult(transport http.RoundTripper, req *http.Request) (served, string, error) {
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return served{}, "", err
	}
	data, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	return served{StatusCode: resp.StatusCode, Header: resp.Header}, string(data), errors.Join(readErr, closeErr)
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
		results := make(chan readResult, count)
		for range count {
			req := request(t, "http://provider.test/data")
			go func() {
				resp, body, err := readResponseResult(transport, req)
				results <- readResult{resp: resp, body: body, err: err}
			}()
		}
		synctest.Wait()
		close(gate)
		for range count {
			result := <-results
			require.NoError(t, result.err)
			require.Empty(t, result.resp.Header.Get(syntheticHeader))
			require.Equal(t, "fresh", result.body)
		}
		require.EqualValues(t, 1, unconditional.Load())
	})
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
	c, _ := testCache(t, DefaultOptions())
	cause := errors.New("synthetic dial failure")
	upstream := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: cause}
	}}
	defer upstream.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req := requestWith(t, ctx, "http://synthetic-user:SECRETPASSWORD@127.0.0.1:1/?api_key=SECRETVALUE")
	resp, err := c.Wrap(upstream).RoundTrip(req)
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}
	require.Nil(t, resp)
	require.Error(t, err)
	require.ErrorIs(t, err, cause)
	require.NotContains(t, err.Error(), "SECRETVALUE")
	require.NotContains(t, err.Error(), "SECRETPASSWORD")
	var operation *net.OpError
	require.ErrorAs(t, err, &operation)
}

func TestCacheHeadGzipKeepsCacheEligibility(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	calls := 0
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, "", "Cache-Control", "max-age=60", "Content-Encoding", "gzip"), nil
	}))
	for range 2 {
		req := request(t, "http://provider.test/data")
		req.Method = http.MethodHead
		resp, body := readResponse(t, transport, req)
		require.Empty(t, body)
		require.False(t, hasDirective(resp.Header, "no-store"))
	}
	// The pinned engine bypasses HEAD before cache lookup.
	require.Equal(t, 2, calls)
}

func TestCacheGzip304DoesNotRefetch(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	calls := 0
	compressed := gzipBody(t, "metadata")
	transport := c.Wrap(transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, string(compressed), "Cache-Control", "max-age=60", "Age", "61", "ETag", `"v1"`, "Content-Encoding", "gzip"), nil
		}
		if got := req.Header.Get("If-None-Match"); got != `"v1"` {
			t.Errorf("validator = %q, want v1; unexpected unconditional refetch", got)
		}
		return response(304, "", "Cache-Control", "max-age=60", "Content-Encoding", "gzip"), nil
	}))
	readResponse(t, transport, request(t, "http://provider.test/data"))
	resp, body := readResponse(t, transport, request(t, "http://provider.test/data"))
	require.Equal(t, string(compressed), body)
	require.Equal(t, "REVALIDATED", resp.Header.Get("X-Httpcache-Status"))
	require.Equal(t, 2, calls)
}
