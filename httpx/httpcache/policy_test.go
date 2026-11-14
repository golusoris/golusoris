// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/testutil/httpclient"
)

func TestOptionsValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		change func(*Options)
	}{
		{"body zero", func(o *Options) { o.MaxBodyBytes = 0 }},
		{"body negative", func(o *Options) { o.MaxBodyBytes = -1 }},
		{"body overflow", func(o *Options) { o.MaxBodyBytes = 1<<63 - 1 }},
		{"inflight zero", func(o *Options) { o.MaxInFlight = 0 }},
		{"inflight negative", func(o *Options) { o.MaxInFlight = -1 }},
		{"revalidate", func(o *Options) { o.RevalidateTimeout = -1 }},
		{"store timeout", func(o *Options) { o.StoreTimeout = -1 }},
		{"stale error", func(o *Options) { o.StaleIfError = -1 }},
		{"stale revalidate", func(o *Options) { o.StaleWhileRevalidate = -1 }},
		{"l1 ttl", func(o *Options) { o.Store.L1TTL = -1 }},
		{"retention negative", func(o *Options) { o.Store.Retention = -1 }},
		{"retention short", func(o *Options) { o.Store.Retention = time.Minute }},
		{"l2", func(o *Options) { o.Store.L2 = "unknown" }},
		{"host empty", func(o *Options) { o.Hosts = []HostOptions{{}} }},
		{"host duplicate", func(o *Options) { o.Hosts = []HostOptions{{Host: "x"}, {Host: "X"}} }},
		{"host ttl", func(o *Options) { o.Hosts = []HostOptions{{Host: "x", TTL: -1}} }},
		{"host stale error", func(o *Options) { o.Hosts = []HostOptions{{Host: "x", StaleIfError: -1}} }},
		{"host stale revalidate", func(o *Options) { o.Hosts = []HostOptions{{Host: "x", StaleWhileRevalidate: -1}} }},
		{"host retention", func(o *Options) { o.Hosts = []HostOptions{{Host: "x", TTL: 24 * time.Hour}} }},
	}
	_, tt := testCache(t, DefaultOptions())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := DefaultOptions()
			tc.change(&opts)
			c, err := New(opts, tt, slog.New(slog.DiscardHandler))
			require.Error(t, err)
			require.Nil(t, c)
		})
	}
	_, err := New(DefaultOptions(), nil, slog.New(slog.DiscardHandler))
	require.Error(t, err)
	_, err = New(DefaultOptions(), tt, nil)
	require.Error(t, err)
	opts := DefaultOptions()
	opts.Store.Retention = 0
	c, err := New(opts, tt, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.NoError(t, c.Close())
	opts.Store.Retention = time.Hour
	require.NoError(t, validateOptions(opts))
}

func TestOptionsExtensions(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.SecretParams = []string{"extra_param"}
	opts.SecretHeaders = []string{"Extra-Header"}
	opts.CredentialFields = []string{"extra.field"}
	opts.AuthPathSegments = []string{"extra_path"}
	opts.Hosts = []HostOptions{{Host: "provider.test", AuthPathSegments: []string{"host_path"}}}
	c, _ := testCache(t, opts)
	for _, key := range []string{"apikey", "extraparam"} {
		require.Contains(t, c.secrets, key)
	}
	for _, key := range []string{"authorization", "extra-header"} {
		require.Contains(t, c.secretHeaders, key)
	}
	for _, key := range []string{"login", "extra_path", "host_path"} {
		require.True(t, c.bypass(request(t, "http://provider.test/"+key)))
	}
	require.False(t, c.bypass(request(t, "http://other.test/host_path")))
	require.True(t, c.credentials.MatchString(`{"access_token": "x"}`))
	require.True(t, c.credentials.MatchString(`{"extra.field": "x"}`))
	require.False(t, c.credentials.MatchString(`{"extraXfield": "x"}`))
	host := c.host(request(t, "http://provider.test/"))
	require.Equal(t, opts.StaleIfError, host.StaleIfError)
}

func TestPolicyCredentialBoundaries(t *testing.T) {
	t.Parallel()
	pattern := credentialPattern(nil)
	for _, body := range []string{`{"accessToken":1}`, `{"refresh-token":1}`, `?X-Plex-Token=x`, `{"access_token": "x"}`, `<token>`, `<TOKEN value>`, `<token/>`, `x token =x`, `?pin=x`, `&password=x`, `<x token="x">`} {
		require.True(t, pattern.MatchString(body), body)
	}
	for _, body := range []string{`{"access_tokens": 1}`, `<tokensuffix>`, `pinned=true`, `a_token=x`, `tokenizer=ok`, `"token"`} {
		require.False(t, pattern.MatchString(body), body)
	}
}

func TestPolicyTTLAndVary(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	resp := response(200, "ok", "Cache-Control", "private, max-age=10, s-maxage=12, no-cache, must-revalidate, proxy-revalidate", "Expires", "old", "Pragma", "no-cache", "ETag", `"v1"`, "Last-Modified", "date", "Vary", "AUTHORIZATION, Accept")
	defer func() { require.NoError(t, resp.Body.Close()) }()
	nonShared := *resp
	c.applyPolicy(resp, []byte("ok"), HostOptions{TTL: time.Minute, StaleIfError: time.Hour, Shared: true}, false)
	require.Equal(t, "private, max-age=60, stale-if-error=3600", resp.Header.Get("Cache-Control"))
	require.Empty(t, resp.Header.Get("Expires"))
	require.Empty(t, resp.Header.Get("Pragma"))
	require.Equal(t, `"v1"`, resp.Header.Get("ETag"))
	require.Equal(t, "date", resp.Header.Get("Last-Modified"))
	require.Equal(t, "Accept", resp.Header.Get("Vary"))
	c.applyPolicy(&nonShared, []byte("ok"), HostOptions{TTL: time.Minute, StaleIfError: time.Hour}, false)
	require.True(t, hasDirective(nonShared.Header, "no-store"))
	require.Contains(t, nonShared.Header.Get("Cache-Control"), "max-age=10")
	require.NotContains(t, nonShared.Header.Get("Cache-Control"), "max-age=60")
	for _, vary := range []string{"Authorization", "*", ""} {
		header := http.Header{"Vary": {vary}}
		c.cleanVary(header)
		if vary == "*" {
			require.Equal(t, "*", header.Get("Vary"))
		} else {
			require.Empty(t, header.Get("Vary"))
		}
	}
	for _, code := range []int{200, 302, 404, 500} {
		r := response(code, "ok", "Cache-Control", "stale-if-error=7, stale-while-revalidate=3")
		c.applyPolicy(r, nil, HostOptions{TTL: time.Minute, StaleIfError: time.Hour, StaleWhileRevalidate: time.Minute}, false)
		require.Contains(t, r.Header.Get("Cache-Control"), "stale-if-error=7")
		require.Contains(t, r.Header.Get("Cache-Control"), "stale-while-revalidate=3")
		require.Equal(t, code != 500, hasDirective(r.Header, "max-age"))
		require.NoError(t, r.Body.Close())
	}
}

func TestCacheDisabledAndNilTransport(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.Hosts = []HostOptions{{Host: "provider.test", Disabled: true}}
	c, _ := testCache(t, opts)
	calls := 0
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(200, "ok"), nil }))
	for range 2 {
		resp, _ := readResponse(t, transport, request(t, "http://provider.test/data"))
		require.Empty(t, resp.Header.Get("X-Httpcache-Status"))
	}
	require.Equal(t, 2, calls)
	require.Error(t, httpclient.RoundTripError(t, c.Wrap(nil), request(t, "http://provider.test/data")))
	require.NoError(t, c.Close())
	require.Error(t, httpclient.RoundTripError(t, c.Wrap(transport), request(t, "http://provider.test/data")))
}

// Compile-time proof that streaming bodies satisfy the transport ownership contract.
var _ io.ReadCloser = (*streamBody)(nil)

func TestPolicyQuotedDirectives(t *testing.T) {
	t.Parallel()
	header := http.Header{"Cache-Control": {`no-cache="ETag, Last-Modified", private="x, y", max-age=7`}}
	overrideTTL(header, time.Minute)
	require.Equal(t, `private="x, y", max-age=60`, header.Get("Cache-Control"))
	require.False(t, hasDirective(http.Header{"Cache-Control": {`private="a, no-store"`}}, "no-store"))
	require.True(t, hasDirective(http.Header{"Cache-Control": {`private="a, x", NO-STORE`}}, "no-store"))
}

func gzipBody(t *testing.T, body string) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	_, err := writer.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return out.Bytes()
}

func TestPolicyEncodedBodies(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.MaxBodyBytes = 256
	c, _ := testCache(t, opts)
	for _, tc := range []struct {
		name, encoding string
		contentType    string
		body           []byte
		stored         bool
	}{
		{"gzip secret", "gzip", "", gzipBody(t, `{"access_token":"`+string(bytes.Repeat([]byte("x"), 200))+`"}`), false},
		{"gzip metadata", " GZip ", "", gzipBody(t, `{"name":"metadata"}`), true},
		{"x-gzip", "x-gzip", "", gzipBody(t, "metadata"), true},
		{"brotli", "br", "", []byte("opaque"), false},
		{"multiple", "gzip, br", "", gzipBody(t, "metadata"), false},
		{"expansion", "gzip", "", gzipBody(t, string(bytes.Repeat([]byte("x"), 257))), false},
		{"invalid", "gzip", "", []byte("invalid gzip"), false},
		{"identity", "identity", "", []byte("metadata"), true},
		{"escaped key", "", "application/json", []byte(`{"\u0061ccess_token":"x"}`), false},
		{"escaped value", "", "application/problem+json; charset=utf-8", []byte(`{"a":"\u0041"}`), true},
		{"form key", "", "application/x-www-form-urlencoded", []byte("access%5Ftoken=x"), false},
		{"form benign", "", "application/x-www-form-urlencoded", []byte("page=2"), true},
		{"form invalid", "", "application/x-www-form-urlencoded", []byte("page=%xx"), false},
		{"json invalid", "", "application/json", []byte(`{"a":"\u0041"`), false},
		{"nested key", "", "application/json", []byte(`{"a":[{"\u0061ccess_token":"x"}]}`), false},
		{"array values", "", "application/json", []byte(`["\u0061ccess_token", {"a":"b"}]`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := response(200, "", "Cache-Control", "max-age=60", "Content-Encoding", tc.encoding, "Content-Type", tc.contentType)
			c.applyPolicy(resp, tc.body, HostOptions{}, false)
			require.Equal(t, !tc.stored, hasDirective(resp.Header, "no-store"))
			require.NoError(t, resp.Body.Close())
		})
	}
}

func TestCacheCredentialNameVariants(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	for _, name := range []string{"accessToken", "access-token", "access_token", "X-Plex-Token", "refresh_token", "id_token"} {
		req := request(t, "http://provider.test/data?"+name+"=synthetic")
		c.stripSecrets(req, &requestState{})
		require.Empty(t, req.URL.RawQuery, name)
	}
	header := http.Header{"Vary": {"X-Plex-Token, Accept"}}
	c.cleanVary(header)
	require.Equal(t, "Accept", header.Get("Vary"))
}

func TestPolicyRedirectCredentials(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"Location", "Content-Location"} {
		for _, tc := range []struct {
			target string
			stored bool
		}{
			{"https://x/?api_key=S", false},
			{"https://u:p@x/", false},
			{"https://x/next?page=2", true},
			{"https://x/?x=1;accessToken=S", false},
			{"https://x/#access_token=S", false},
			{"https://x/#a=1&accessToken=S", false},
			{"https://x/#%61ccess%5Ftoken=S", false},
			{"https://x/page#section-2", true},
		} {
			c, _ := testCache(t, DefaultOptions())
			calls := 0
			transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(302, "redirect", "Cache-Control", "max-age=60", header, tc.target), nil
			}))
			for range 2 {
				readResponse(t, transport, request(t, "http://provider.test/data"))
			}
			expected := 2
			if tc.stored {
				expected = 1
			}
			require.Equal(t, expected, calls, header+" "+tc.target)
		}
	}
}

func TestCacheHostNames(t *testing.T) {
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
		opts := DefaultOptions()
		opts.Hosts = []HostOptions{{Host: tc.name}}
		require.Equal(t, tc.valid, validateOptions(opts) == nil, tc.name)
	}
}

func TestPolicyDurationRounding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		duration time.Duration
		seconds  string
	}{
		{time.Nanosecond, "1"}, {time.Second, "1"}, {1500 * time.Millisecond, "2"}, {time.Duration(1<<63 - 1), "9223372037"},
	} {
		header := make(http.Header)
		overrideTTL(header, tc.duration)
		require.Equal(t, "max-age="+tc.seconds, header.Get("Cache-Control"))
		for _, directive := range []string{"stale-if-error", "stale-while-revalidate"} {
			stale := make(http.Header)
			addStale(stale, directive, tc.duration)
			require.Equal(t, directive+"="+tc.seconds, stale.Get("Cache-Control"))
		}
	}
}

func TestPolicyBodylessGzip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method    string
		status    int
		oversized bool
		noStore   bool
	}{
		{http.MethodHead, 200, false, false},
		{http.MethodGet, 304, false, false},
		{http.MethodGet, 204, false, false},
		{http.MethodGet, 100, false, false},
		{http.MethodGet, 199, false, false},
		{http.MethodGet, 200, false, true},
		{http.MethodHead, 200, true, true},
		{http.MethodGet, 304, true, true},
	} {
		c, _ := testCache(t, DefaultOptions())
		resp := response(tc.status, "", "Content-Encoding", "gzip")
		resp.Request = request(t, "http://provider.test/data")
		resp.Request.Method = tc.method
		c.applyBody(resp, nil, tc.oversized)
		require.Equal(t, tc.noStore, hasDirective(resp.Header, "no-store"), "%s %d oversized=%t", tc.method, tc.status, tc.oversized)
		require.NoError(t, resp.Body.Close())
	}
}

func TestPolicyNoStoreUnclosedQuote(t *testing.T) {
	t.Parallel()
	c, _ := testCache(t, DefaultOptions())
	for _, tc := range []struct {
		name      string
		control   []string
		want      string
		oversized bool
	}{
		{"unclosed quote", []string{`max-age=60, s-maxage="60`}, `no-store, max-age=60, s-maxage="60`, false},
		{"empty", nil, "no-store", false},
		{"empty value", []string{""}, "no-store", false},
		{"existing", []string{"no-store, max-age=60"}, "no-store, max-age=60", false},
		{"multiple lines", []string{"max-age=60", `s-maxage="60`}, `no-store, max-age=60, s-maxage="60`, false},
		{"oversized unclosed quote", []string{`max-age=60, s-maxage="60`}, `no-store, max-age=60, s-maxage="60`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := response(200, "ok", "Vary", "Authorization", "Expires", "old")
			defer func() { require.NoError(t, resp.Body.Close()) }()
			resp.Header["Cache-Control"] = tc.control
			c.applyPolicy(resp, []byte("ok"), HostOptions{}, tc.oversized)
			t.Logf("after applyPolicy: Cache-Control=%q directives=%q no-store=%t", resp.Header.Get("Cache-Control"), cacheDirectives(resp.Header.Get("Cache-Control")), hasDirective(resp.Header, "no-store"))
			require.True(t, hasDirective(resp.Header, "no-store"))
			require.Equal(t, tc.want, resp.Header.Get("Cache-Control"))
			require.Empty(t, resp.Header.Get("Expires"))
		})
	}
}
