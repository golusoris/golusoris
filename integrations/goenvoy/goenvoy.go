// Package goenvoy wires opt-in goenvoy service clients (Sonarr, TMDb, AniList,
// Trakt, ...) onto the framework's resilient outbound *http.Client. It is a
// thin fx adapter — it does NOT reimplement goenvoy.
//
// Each configured service gets its OWN *http.Client built from [httpx/client]
// (retry + circuit-breaker + otelhttp + slog). One client per service is
// mandatory: goenvoy's arr.WithTimeout / metadata.WithTimeout mutate
// httpClient.Timeout in place, so a shared transport would corrupt timeouts.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    memory.Module,    // optional: enables the HTTP response cache
//	    goenvoy.Module,   // provides *goenvoy.Registry
//	    fx.Invoke(func(r *goenvoy.Registry) error {
//	        sonarr, err := r.Sonarr("sonarr")
//	        ...
//	    }),
//	)
//
// Config (env: APP_INTEGRATIONS_GOENVOY_*):
//
//	integrations.goenvoy.services.sonarr.provider     = "sonarr"
//	integrations.goenvoy.services.sonarr.base_url      = "http://sonarr:8989"
//	integrations.goenvoy.services.sonarr.api_key        = "${SONARR_API_KEY}"
//	integrations.goenvoy.services.sonarr.timeout        = "10s"
//	integrations.goenvoy.services.sonarr.cache_ttl       = "30s"
//	integrations.goenvoy.services.tmdb.provider          = "tmdb"
//	integrations.goenvoy.services.tmdb.access_token      = "${TMDB_V4_TOKEN}"
package goenvoy

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/golusoris/goenvoy/arr/sonarr"
	arr "github.com/golusoris/goenvoy/arr/v2"
	"github.com/golusoris/goenvoy/metadata"
	"github.com/golusoris/goenvoy/metadata/anime/anilist"
	"github.com/golusoris/goenvoy/metadata/tracking/trakt"
	"github.com/golusoris/goenvoy/metadata/video/tmdb"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/httpx/client"
)

// Provider identifies which goenvoy client a service entry builds.
const (
	ProviderSonarr  = "sonarr"
	ProviderTMDb    = "tmdb"
	ProviderAniList = "anilist"
	ProviderTrakt   = "trakt"
)

// ServiceOptions configures a single upstream goenvoy client. Which fields
// matter depends on Provider (arr uses APIKey; tmdb uses AccessToken; trakt
// uses ClientID + optional secret/token).
type ServiceOptions struct {
	// Provider selects the goenvoy client: "sonarr"|"tmdb"|"anilist"|"trakt".
	Provider string `koanf:"provider"`
	// BaseURL is the upstream origin (required for arr; optional override for
	// metadata providers that ship a default base URL).
	BaseURL string `koanf:"base_url"`
	// APIKey is the arr X-Api-Key credential.
	APIKey string `koanf:"api_key"`
	// AccessToken is the tmdb v4 bearer / anilist OAuth / trakt OAuth token.
	AccessToken string `koanf:"access_token"`
	// ClientID is the trakt application client id (sent as Trakt-Api-Key).
	ClientID string `koanf:"client_id"`
	// ClientSecret is the trakt OAuth client secret.
	ClientSecret string `koanf:"client_secret"`
	// UserAgent overrides the User-Agent sent to the upstream.
	UserAgent string `koanf:"user_agent"`
	// Timeout caps a single request (0 = httpx/client default of 30s).
	Timeout time.Duration `koanf:"timeout"`
	// CacheTTL, when > 0, enables an HTTP response cache for GET requests
	// (requires a *memory.Cache; without one, caching is silently disabled).
	CacheTTL time.Duration `koanf:"cache_ttl"`
	// Retry mirrors httpx/client retry policy. Zero disables retries.
	Retry client.RetryOptions `koanf:"retry"`
	// Breaker mirrors httpx/client circuit-breaker policy. Zero disables it.
	Breaker client.BreakerOptions `koanf:"breaker"`
}

// factory builds resilient *http.Client instances and goenvoy clients from
// the configured services. It is the single seam where the framework's
// outbound stack and optional cache are injected into goenvoy.
type factory struct {
	services map[string]ServiceOptions
	logger   *slog.Logger
	clk      clock.Clock
	cache    cacheStore
	newHTTP  func(client.Options) *http.Client // injectable for tests

	mu    sync.Mutex
	built []*http.Client // every client created, for CloseIdleConnections on stop
}

// cacheStore is the minimal cache surface the cacheTransport needs; it lets
// the factory accept *memory.Cache (or nil) without importing it when the
// caller wires no cache.
type cacheStore interface {
	GetIfPresent(key string) (any, bool)
	Set(key string, value any) (any, bool)
}

// svc returns the options for a named service, or a descriptive error.
func (f *factory) svc(name string) (ServiceOptions, error) {
	o, ok := f.services[name]
	if !ok {
		return ServiceOptions{}, fmt.Errorf("goenvoy: no service %q configured (have: %v)", name, f.names())
	}
	return o, nil
}

func (f *factory) names() []string {
	out := make([]string, 0, len(f.services))
	for n := range f.services {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// httpClient builds one resilient *http.Client for a service. It is never
// shared between goenvoy clients because goenvoy's WithTimeout mutates the
// client in place. When CacheTTL > 0 and a cache is present, the transport is
// wrapped with a clock-driven caching RoundTripper.
func (f *factory) httpClient(name string, o ServiceOptions) *http.Client {
	hc := f.newHTTP(client.Options{
		Name:    "goenvoy:" + name,
		Timeout: o.Timeout,
		Retry:   o.Retry,
		Breaker: o.Breaker,
		Logger:  f.logger,
	})
	if o.CacheTTL > 0 && f.cache != nil {
		hc.Transport = &cacheTransport{
			next:  hc.Transport,
			cache: f.cache,
			clk:   f.clk,
			ttl:   o.CacheTTL,
			scope: "goenvoy:" + name,
		}
	}
	f.mu.Lock()
	f.built = append(f.built, hc)
	f.mu.Unlock()
	return hc
}

// closeIdle releases pooled connections for every *http.Client this factory
// built. Called from the module's OnStop hook.
func (f *factory) closeIdle() {
	f.mu.Lock()
	clients := append([]*http.Client(nil), f.built...)
	f.mu.Unlock()
	for _, c := range clients {
		c.CloseIdleConnections()
	}
}

// userAgent returns the configured UA or a stable default.
func (o ServiceOptions) userAgent() string {
	if o.UserAgent != "" {
		return o.UserAgent
	}
	return "golusoris-goenvoy"
}

// newSonarr builds a Sonarr client over a dedicated resilient transport. The
// timeout is applied via client.Options, NOT arr.WithTimeout, to avoid
// mutating the shared transport.
func (f *factory) newSonarr(name string) (*sonarr.Client, error) {
	o, err := f.svc(name)
	if err != nil {
		return nil, err
	}
	c, err := sonarr.New(
		o.BaseURL, o.APIKey,
		arr.WithHTTPClient(f.httpClient(name, o)),
		arr.WithUserAgent(o.userAgent()),
	)
	if err != nil {
		return nil, fmt.Errorf("goenvoy: build sonarr %q: %w", name, err)
	}
	return c, nil
}

// newTMDb builds a TMDb client. AccessToken is the v4 bearer credential.
func (f *factory) newTMDb(name string) (*tmdb.Client, error) {
	o, err := f.svc(name)
	if err != nil {
		return nil, err
	}
	opts := []metadata.Option{
		metadata.WithHTTPClient(f.httpClient(name, o)),
		metadata.WithUserAgent(o.userAgent()),
	}
	if o.BaseURL != "" {
		opts = append(opts, metadata.WithBaseURL(o.BaseURL))
	}
	return tmdb.New(o.AccessToken, opts...), nil
}

// newAniList builds an AniList client, authenticated when an AccessToken is set.
func (f *factory) newAniList(name string) (*anilist.Client, error) {
	o, err := f.svc(name)
	if err != nil {
		return nil, err
	}
	opts := []metadata.Option{
		metadata.WithHTTPClient(f.httpClient(name, o)),
		metadata.WithUserAgent(o.userAgent()),
	}
	if o.BaseURL != "" {
		opts = append(opts, metadata.WithBaseURL(o.BaseURL))
	}
	if o.AccessToken != "" {
		return anilist.NewWithToken(o.AccessToken, opts...), nil
	}
	return anilist.New(opts...), nil
}

// newTrakt builds a Trakt client. ClientID is the Trakt-Api-Key; optional
// secret/token enable authenticated calls. Refresh-token rotation is the app's
// responsibility — this adapter does not persist rotated tokens.
func (f *factory) newTrakt(name string) (*trakt.Client, error) {
	o, err := f.svc(name)
	if err != nil {
		return nil, err
	}
	opts := []metadata.Option{
		metadata.WithHTTPClient(f.httpClient(name, o)),
		metadata.WithUserAgent(o.userAgent()),
	}
	if o.BaseURL != "" {
		opts = append(opts, metadata.WithBaseURL(o.BaseURL))
	}
	c := trakt.New(o.ClientID, opts...)
	if o.ClientSecret != "" {
		c.SetClientSecret(o.ClientSecret)
	}
	if o.AccessToken != "" {
		c.SetAccessToken(o.AccessToken)
	}
	return c, nil
}

// realClock returns the injected clock or a real one when none was wired.
func realClock(c clock.Clock) clock.Clock {
	if c == nil {
		return clockwork.NewRealClock()
	}
	return c
}
