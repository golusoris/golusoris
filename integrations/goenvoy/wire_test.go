package goenvoy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/httpx/client"
	"github.com/golusoris/golusoris/integrations/goenvoy"
)

// newCache builds an in-process cache for the transport tests (no otter TTL —
// expiry is driven by the goenvoy cacheTransport's injected clock).
func newCache(t *testing.T) *memory.Cache {
	t.Helper()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	return c
}

// TestSonarrWiring stands up a fake Sonarr and asserts the X-Api-Key header
// reaches it through the injected resilient transport.
func TestSonarrWiring(t *testing.T) {
	t.Parallel()
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"source":"x","type":"ok","message":"fine"}]`))
	}))
	defer srv.Close()

	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"sonarr": {Provider: goenvoy.ProviderSonarr, BaseURL: srv.URL, APIKey: "secret-key"},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	c, err := r.Sonarr("sonarr")
	if err != nil {
		t.Fatalf("Sonarr: %v", err)
	}
	if _, err := c.GetHealth(context.Background()); err != nil {
		t.Fatalf("GetHealth: %v", err)
	}
	if gotKey != "secret-key" {
		t.Fatalf("X-Api-Key not propagated: %q", gotKey)
	}
}

// TestTMDbWiring asserts the Authorization: Bearer header and that WithBaseURL
// points the client at our stub.
func TestTMDbWiring(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"images":{"base_url":"http://img"}}`))
	}))
	defer srv.Close()

	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"tmdb": {Provider: goenvoy.ProviderTMDb, BaseURL: srv.URL, AccessToken: "v4-token"},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	c, err := r.TMDb("tmdb")
	if err != nil {
		t.Fatalf("TMDb: %v", err)
	}
	if _, err := c.GetConfiguration(context.Background()); err != nil {
		t.Fatalf("GetConfiguration: %v", err)
	}
	if gotAuth != "Bearer v4-token" {
		t.Fatalf("Authorization not propagated: %q", gotAuth)
	}
}

// TestAniListWiring asserts the bearer token (NewWithToken path) reaches the
// GraphQL endpoint.
func TestAniListWiring(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"GenreCollection":["Action"]}}`))
	}))
	defer srv.Close()

	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"anilist": {Provider: goenvoy.ProviderAniList, BaseURL: srv.URL, AccessToken: "ani-token"},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	c, err := r.AniList("anilist")
	if err != nil {
		t.Fatalf("AniList: %v", err)
	}
	genres, err := c.GetGenres(context.Background())
	if err != nil {
		t.Fatalf("GetGenres: %v", err)
	}
	if len(genres) != 1 || genres[0] != "Action" {
		t.Fatalf("genres: %v", genres)
	}
	if gotAuth != "Bearer ani-token" {
		t.Fatalf("Authorization not propagated: %q", gotAuth)
	}
}

// TestTraktWiring asserts the Trakt-Api-Key (client id) and the OAuth bearer
// (set via SetAccessToken) both reach the upstream.
func TestTraktWiring(t *testing.T) {
	t.Parallel()
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Trakt-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"Action","slug":"action"}]`))
	}))
	defer srv.Close()

	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"trakt": {
			Provider: goenvoy.ProviderTrakt, BaseURL: srv.URL,
			ClientID: "cid", ClientSecret: "csecret", AccessToken: "trakt-oauth",
		},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	c, err := r.Trakt("trakt")
	if err != nil {
		t.Fatalf("Trakt: %v", err)
	}
	if _, err := c.Genres(context.Background(), "movies"); err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if gotKey != "cid" {
		t.Fatalf("Trakt-Api-Key not propagated: %q", gotKey)
	}
	if gotAuth != "Bearer trakt-oauth" {
		t.Fatalf("Authorization not propagated: %q", gotAuth)
	}
}

// TestResilientTransport_retries asserts the injected retry transport recovers
// a 503 → 200 on a real goenvoy call.
func TestResilientTransport_retries(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"sonarr": {
			Provider: goenvoy.ProviderSonarr, BaseURL: srv.URL, APIKey: "k",
			Retry: client.RetryOptions{Max: 2, Wait: time.Millisecond, MaxWait: 5 * time.Millisecond},
		},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	c, err := r.Sonarr("sonarr")
	if err != nil {
		t.Fatalf("Sonarr: %v", err)
	}
	if _, err := c.GetHealth(context.Background()); err != nil {
		t.Fatalf("GetHealth after retry: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected 2 upstream hits (503 then 200), got %d", got)
	}
}

// TestCacheTransport_ttl drives the clock-based response cache: a second GET
// inside the TTL is served from cache (upstream hit count stays 1), and after
// the TTL elapses the next GET re-fetches.
func TestCacheTransport_ttl(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"images":{"base_url":"http://img"}}`))
	}))
	defer srv.Close()

	fake := clock.NewFake()
	cache := newCache(t)
	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"tmdb": {
			Provider: goenvoy.ProviderTMDb, BaseURL: srv.URL, AccessToken: "t",
			CacheTTL: 30 * time.Second,
		},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), fake, cache, nil)
	c, err := r.TMDb("tmdb")
	if err != nil {
		t.Fatalf("TMDb: %v", err)
	}

	ctx := context.Background()
	if _, err := c.GetConfiguration(ctx); err != nil {
		t.Fatalf("first GET: %v", err)
	}
	if _, err := c.GetConfiguration(ctx); err != nil {
		t.Fatalf("cached GET: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 upstream hit within TTL, got %d", got)
	}

	fake.Advance(31 * time.Second)
	if _, err := c.GetConfiguration(ctx); err != nil {
		t.Fatalf("post-TTL GET: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected re-fetch after TTL (2 hits), got %d", got)
	}
}

// TestCloseIdle exercises the lifecycle cleanup path without fx.
func TestCloseIdle(t *testing.T) {
	t.Parallel()
	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"tmdb": {Provider: goenvoy.ProviderTMDb, AccessToken: "x"},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	if _, err := r.TMDb("tmdb"); err != nil {
		t.Fatalf("TMDb: %v", err)
	}
	r.CloseIdleForTest() // must not panic
}
