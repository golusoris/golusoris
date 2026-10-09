// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package goenvoy

import (
	"net/http"
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
