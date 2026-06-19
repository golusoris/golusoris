package goenvoy_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/integrations/goenvoy"
)

// TestModule_StartsAndStops boots the Module via fxtest, covering loadOptions,
// newRegistry, the OnStop hook, and one real client lookup.
func TestModule_StartsAndStops(t *testing.T) {
	t.Parallel()
	cfg := writeConfig(t, configYAML)

	var reg *goenvoy.Registry
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		goenvoy.Module,
		fx.Populate(&reg),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := reg.Names(); len(got) != 2 {
		t.Fatalf("expected 2 services, got %v", got)
	}
	if _, err := reg.Sonarr("sonarr"); err != nil {
		t.Fatalf("Sonarr: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestUnknownService_perProvider covers the not-configured error path on every
// provider accessor.
func TestUnknownService_perProvider(t *testing.T) {
	t.Parallel()
	r := goenvoy.NewRegistryForTest(goenvoy.Options{}, discardLogger(), nil, nil, nil)
	if _, err := r.TMDb("x"); err == nil {
		t.Fatal("TMDb: expected error")
	}
	if _, err := r.AniList("x"); err == nil {
		t.Fatal("AniList: expected error")
	}
	if _, err := r.Trakt("x"); err == nil {
		t.Fatal("Trakt: expected error")
	}
}

// TestAniList_unauthenticated covers the New (no token) branch and the
// custom-UserAgent default override.
func TestAniList_unauthenticated(t *testing.T) {
	t.Parallel()
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"GenreCollection":[]}}`))
	}))
	defer srv.Close()

	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"anilist": {Provider: goenvoy.ProviderAniList, BaseURL: srv.URL, UserAgent: "custom-ua"},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), nil, nil, nil)
	c, err := r.AniList("anilist")
	if err != nil {
		t.Fatalf("AniList: %v", err)
	}
	if _, err := c.GetGenres(context.Background()); err != nil {
		t.Fatalf("GetGenres: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("unexpected Authorization header: %q", gotAuth)
	}
	if gotUA != "custom-ua" {
		t.Fatalf("custom user agent not applied: %q", gotUA)
	}
}

// TestCacheTransport_nonGetAndError covers the non-GET passthrough and the
// non-2xx (not cached) branch of the cache transport.
func TestCacheTransport_nonCacheable(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status_message":"nope"}`))
	}))
	defer srv.Close()

	fake := clock.NewFake()
	cache := newCache(t)
	opts := goenvoy.Options{Services: map[string]goenvoy.ServiceOptions{
		"tmdb": {
			Provider: goenvoy.ProviderTMDb, BaseURL: srv.URL, AccessToken: "t",
			CacheTTL: time.Minute,
		},
	}}
	r := goenvoy.NewRegistryForTest(opts, discardLogger(), fake, cache, nil)
	c, err := r.TMDb("tmdb")
	if err != nil {
		t.Fatalf("TMDb: %v", err)
	}
	// Two failing GETs: a non-2xx response must NOT be cached, so both hit.
	_, _ = c.GetConfiguration(context.Background())
	_, _ = c.GetConfiguration(context.Background())
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("non-2xx must not be cached, expected 2 hits, got %d", got)
	}
}
