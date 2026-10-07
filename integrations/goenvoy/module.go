// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package goenvoy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/golusoris/goenvoy/arr/sonarr"
	"github.com/golusoris/goenvoy/metadata/anime/anilist"
	"github.com/golusoris/goenvoy/metadata/tracking/trakt"
	"github.com/golusoris/goenvoy/metadata/video/tmdb"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/singleflight"
	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/httpx/client"
)

// Options is the koanf-bound config under the integrations.goenvoy prefix. Each
// entry in Services configures one upstream goenvoy client.
type Options struct {
	// Services maps a logical service name to its per-upstream options.
	Services map[string]ServiceOptions `koanf:"services"`
}

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("integrations.goenvoy", &opts); err != nil {
		return Options{}, fmt.Errorf("goenvoy: load options: %w", err)
	}
	return opts, nil
}

// Registry hands out goenvoy clients by configured service name, building each
// over its own resilient transport. Clients are cached per name so repeated
// lookups return the same instance.
type Registry struct {
	f             *factory
	mu            sync.RWMutex
	sonarr        map[string]*sonarr.Client
	tmdb          map[string]*tmdb.Client
	anilist       map[string]*anilist.Client
	trakt         map[string]*trakt.Client
	sonarrBuilds  *singleflight.Group[string, *sonarr.Client]
	tmdbBuilds    *singleflight.Group[string, *tmdb.Client]
	anilistBuilds *singleflight.Group[string, *anilist.Client]
	traktBuilds   *singleflight.Group[string, *trakt.Client]
}

// Sonarr returns the Sonarr client configured under name.
func (r *Registry) Sonarr(name string) (*sonarr.Client, error) {
	return registryClient(&r.mu, r.sonarr, r.sonarrBuilds, name, func() (*sonarr.Client, error) {
		return r.f.newSonarr(name)
	})
}

// TMDb returns the TMDb client configured under name.
func (r *Registry) TMDb(name string) (*tmdb.Client, error) {
	return registryClient(&r.mu, r.tmdb, r.tmdbBuilds, name, func() (*tmdb.Client, error) {
		return r.f.newTMDb(name)
	})
}

// AniList returns the AniList client configured under name.
func (r *Registry) AniList(name string) (*anilist.Client, error) {
	return registryClient(&r.mu, r.anilist, r.anilistBuilds, name, func() (*anilist.Client, error) {
		return r.f.newAniList(name)
	})
}

// Trakt returns the Trakt client configured under name.
func (r *Registry) Trakt(name string) (*trakt.Client, error) {
	return registryClient(&r.mu, r.trakt, r.traktBuilds, name, func() (*trakt.Client, error) {
		return r.f.newTrakt(name)
	})
}

// Names returns the configured service names in sorted order.
func (r *Registry) Names() []string { return r.f.names() }

// registryParams are the fx inputs for [newRegistry]. Clock, Cache, and the
// underlying HTTP factory are optional so the module works without
// clock.Module, memory.Module, or OTel wired.
type registryParams struct {
	fx.In

	Opts   Options
	Logger *slog.Logger
	Clk    clock.Clock   `optional:"true"`
	Cache  *memory.Cache `optional:"true"`
}

func newRegistry(p registryParams) *Registry {
	logger := loggerOrDiscard(p.Logger)
	var store cacheStore
	if p.Cache != nil {
		store = p.Cache
	}
	f := &factory{
		services: p.Opts.Services,
		logger:   logger,
		clk:      realClock(p.Clk),
		cache:    store,
		newHTTP:  client.New,
	}
	logger.Debug(
		"goenvoy: started",
		slog.Int("services", len(p.Opts.Services)),
		slog.Bool("cache", p.Cache != nil),
	)
	return newRegistryFromFactory(f)
}

func newRegistryFromFactory(f *factory) *Registry {
	return &Registry{
		f:             f,
		sonarr:        make(map[string]*sonarr.Client),
		tmdb:          make(map[string]*tmdb.Client),
		anilist:       make(map[string]*anilist.Client),
		trakt:         make(map[string]*trakt.Client),
		sonarrBuilds:  singleflight.New[string, *sonarr.Client](),
		tmdbBuilds:    singleflight.New[string, *tmdb.Client](),
		anilistBuilds: singleflight.New[string, *anilist.Client](),
		traktBuilds:   singleflight.New[string, *trakt.Client](),
	}
}

func registryClient[T any](
	mu *sync.RWMutex,
	clients map[string]T,
	builds *singleflight.Group[string, T],
	name string,
	build func() (T, error),
) (T, error) {
	mu.RLock()
	value, ok := clients[name]
	mu.RUnlock()
	if ok {
		return value, nil
	}

	value, _, err := builds.Do(context.Background(), name, func(context.Context) (T, error) {
		mu.RLock()
		cached, found := clients[name]
		mu.RUnlock()
		if found {
			return cached, nil
		}

		created, buildErr := build()
		if buildErr != nil {
			var zero T
			return zero, buildErr
		}
		mu.Lock()
		clients[name] = created
		mu.Unlock()
		return created, nil
	})
	if err != nil {
		var zero T
		return zero, fmt.Errorf("goenvoy: build client %q: %w", name, err)
	}
	return value, nil
}

// Module provides a *Registry to the fx graph and closes idle connections on
// stop. No init() side effects — all construction happens in fx constructors.
var Module = fx.Module(
	"golusoris.integrations.goenvoy",
	fx.Provide(loadOptions),
	fx.Provide(newRegistry),
	fx.Invoke(func(lc fx.Lifecycle, r *Registry) {
		lc.Append(fx.Hook{
			OnStop: func(_ context.Context) error {
				r.closeIdle()
				return nil
			},
		})
	}),
)

// closeIdle releases pooled connections for every built client on shutdown.
func (r *Registry) closeIdle() { r.f.closeIdle() }

// CloseIdleForTest exposes the shutdown path to external tests.
func (r *Registry) CloseIdleForTest() { r.closeIdle() }

// NewRegistryForTest builds a Registry from explicit options without fx, for
// tests. clk/cache may be nil; newHTTP may be nil to use the real factory.
func NewRegistryForTest(
	opts Options,
	logger *slog.Logger,
	clk clock.Clock,
	cache cacheStore,
	newHTTP func(client.Options) *http.Client,
) *Registry {
	if newHTTP == nil {
		newHTTP = client.New
	}
	logger = loggerOrDiscard(logger)
	f := &factory{
		services: opts.Services,
		logger:   logger,
		clk:      realClock(clk),
		cache:    optionalCache(cache),
		newHTTP:  newHTTP,
	}
	return newRegistryFromFactory(f)
}

func optionalCache(cache cacheStore) cacheStore {
	if validate.IsNil(cache) {
		return nil
	}
	return cache
}

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return logger
}
