package goenvoy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golusoris/golusoris/clock"
)

// maxCacheBytes caps how much of a response body the cacheTransport buffers,
// guarding against a hostile upstream streaming an unbounded body into memory.
const maxCacheBytes = 8 << 20 // 8 MiB

// cachedResponse is the stored form of a cacheable GET response. expiresAt is
// computed from the injected clock so TTL expiry is deterministic in tests.
type cachedResponse struct {
	status    int
	header    http.Header
	body      []byte
	expiresAt time.Time
}

// cacheTransport is a RoundTripper that caches GET responses keyed by full URL
// for a bounded TTL. It is intentionally conservative: only GET, only 2xx, and
// the cache key includes the Authorization/X-Api-Key/Trakt-Api-Key credential
// so responses are never served across distinct credentials.
type cacheTransport struct {
	next  http.RoundTripper
	cache cacheStore
	clk   clock.Clock
	ttl   time.Duration
	scope string
}

// RoundTrip serves a cached response when fresh, otherwise forwards to next and
// caches a successful GET.
func (t *cacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.forward(req)
	}
	key := t.key(req)
	if resp, ok := t.load(key); ok {
		return resp, nil
	}
	resp, err := t.forward(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	return t.store(key, resp)
}

func (t *cacheTransport) forward(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("goenvoy: cache transport: %w", err)
	}
	return resp, nil
}

// key derives a credential-aware cache key so a different token never reads
// another token's cached response.
func (t *cacheTransport) key(req *http.Request) string {
	cred := req.Header.Get("Authorization") +
		"|" + req.Header.Get("X-Api-Key") +
		"|" + req.Header.Get("Trakt-Api-Key")
	return fmt.Sprintf("%s|%s|%s", t.scope, req.URL.String(), cred)
}

// load returns a fresh cached response, or false when absent/expired.
func (t *cacheTransport) load(key string) (*http.Response, bool) {
	v, ok := t.cache.GetIfPresent(key)
	if !ok {
		return nil, false
	}
	entry, ok := v.(cachedResponse)
	if !ok {
		return nil, false
	}
	if t.clk.Now().After(entry.expiresAt) {
		return nil, false
	}
	return t.response(entry), true
}

// store buffers, caches, and rebuilds the response so the caller still gets a
// readable body after we consumed it.
func (t *cacheTransport) store(key string, resp *http.Response) (*http.Response, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCacheBytes))
	if cerr := resp.Body.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("goenvoy: cache transport: read body: %w", err)
	}
	entry := cachedResponse{
		status:    resp.StatusCode,
		header:    resp.Header.Clone(),
		body:      body,
		expiresAt: t.clk.Now().Add(t.ttl),
	}
	t.cache.Set(key, entry)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// response rebuilds an *http.Response from a cached entry with a fresh body.
func (t *cacheTransport) response(entry cachedResponse) *http.Response {
	return &http.Response{
		StatusCode: entry.status,
		Status:     http.StatusText(entry.status),
		Header:     entry.header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(entry.body)),
	}
}
