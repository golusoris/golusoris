// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package goenvoy

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/httpx/client"
)

func TestNewRegistry_NilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()
	r := newRegistry(registryParams{})
	if r == nil {
		t.Fatal("newRegistry returned nil")
	}
}

type nopCache struct{}

func (nopCache) GetIfPresent(any) (any, bool) { return nil, false }
func (nopCache) Set(any, any) (any, bool)     { return nil, false }

func newTestFactory(transport http.RoundTripper) *factory {
	return &factory{
		cache: nopCache{},
		newHTTP: func(client.Options) *http.Client {
			return &http.Client{Timeout: time.Second, Transport: transport}
		},
	}
}

// TestHTTPClient_ownsMissingTransport pins that a service client whose builder
// left Transport nil gets a private clone, never http.DefaultTransport (#703).
func TestHTTPClient_ownsMissingTransport(t *testing.T) {
	t.Parallel()
	f := newTestFactory(nil)
	plain := f.httpClient("plain", ServiceOptions{}).Transport
	if _, ok := plain.(*http.Transport); !ok || plain == http.DefaultTransport {
		t.Fatalf("uncached transport = %T shared=%v, want a private *http.Transport", plain, plain == http.DefaultTransport)
	}
	cached, ok := f.httpClient("cached", ServiceOptions{CacheTTL: time.Minute}).Transport.(*cacheTransport)
	if !ok {
		t.Fatal("cached service transport is not *cacheTransport")
	}
	if _, ok := cached.next.(*http.Transport); !ok || cached.next == http.DefaultTransport {
		t.Fatalf("cache next = %T shared=%v, want a private *http.Transport", cached.next, cached.next == http.DefaultTransport)
	}
	if cached.next == plain {
		t.Fatal("two services share one transport")
	}
}

// idleCountingTransport counts CloseIdleConnections calls.
type idleCountingTransport struct {
	http.RoundTripper
	closes atomic.Int32
}

func (c *idleCountingTransport) CloseIdleConnections() { c.closes.Add(1) }

// TestCloseIdle_reachesCachedTransport pins that OnStop's closeIdle reaches the
// pool under every built client, cached or not, and tolerates a transport
// without CloseIdleConnections (#709).
func TestCloseIdle_reachesCachedTransport(t *testing.T) {
	t.Parallel()
	tr := &idleCountingTransport{}
	f := newTestFactory(tr)
	f.httpClient("plain", ServiceOptions{})
	f.httpClient("cached", ServiceOptions{CacheTTL: time.Minute})
	f.closeIdle()
	if got := tr.closes.Load(); got != 2 {
		t.Fatalf("CloseIdleConnections calls = %d, want 2 (plain + cached)", got)
	}
	bare := newTestFactory(roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, http.ErrNotSupported }))
	bare.httpClient("cached", ServiceOptions{CacheTTL: time.Minute})
	bare.closeIdle() // must not panic
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestHTTPClient_keepsBuilderTransport pins the boundary: a transport the
// builder set is wrapped as-is, not replaced.
func TestHTTPClient_keepsBuilderTransport(t *testing.T) {
	t.Parallel()
	explicit := &http.Transport{}
	f := newTestFactory(explicit)
	if got := f.httpClient("plain", ServiceOptions{}).Transport; got != explicit {
		t.Fatalf("uncached transport = %T, want the builder's transport", got)
	}
	cached, ok := f.httpClient("cached", ServiceOptions{CacheTTL: time.Minute}).Transport.(*cacheTransport)
	if !ok || cached.next != explicit {
		t.Fatal("cache does not wrap the builder's transport")
	}
}
