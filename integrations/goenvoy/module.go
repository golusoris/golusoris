package goenvoy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/golusoris/goenvoy/arr/sonarr"
	"github.com/golusoris/goenvoy/metadata/anime/anilist"
	"github.com/golusoris/goenvoy/metadata/tracking/trakt"
	"github.com/golusoris/goenvoy/metadata/video/tmdb"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
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
	f       *factory
	sonarr  map[string]*sonarr.Client
	tmdb    map[string]*tmdb.Client
	anilist map[string]*anilist.Client
	trakt   map[string]*trakt.Client
}

// Sonarr returns the Sonarr client configured under name.
func (r *Registry) Sonarr(name string) (*sonarr.Client, error) {
	if c, ok := r.sonarr[name]; ok {
		return c, nil
	}
	c, err := r.f.newSonarr(name)
	if err != nil {
		return nil, err
	}
	r.sonarr[name] = c
	return c, nil
}

// TMDb returns the TMDb client configured under name.
func (r *Registry) TMDb(name string) (*tmdb.Client, error) {
	if c, ok := r.tmdb[name]; ok {
		return c, nil
	}
	c, err := r.f.newTMDb(name)
	if err != nil {
		return nil, err
	}
	r.tmdb[name] = c
	return c, nil
}

// AniList returns the AniList client configured under name.
func (r *Registry) AniList(name string) (*anilist.Client, error) {
	if c, ok := r.anilist[name]; ok {
		return c, nil
	}
	c, err := r.f.newAniList(name)
	if err != nil {
		return nil, err
	}
	r.anilist[name] = c
	return c, nil
}

// Trakt returns the Trakt client configured under name.
func (r *Registry) Trakt(name string) (*trakt.Client, error) {
	if c, ok := r.trakt[name]; ok {
		return c, nil
	}
	c, err := r.f.newTrakt(name)
	if err != nil {
		return nil, err
	}
	r.trakt[name] = c
	return c, nil
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
	var store cacheStore
	if p.Cache != nil {
		store = p.Cache
	}
	f := &factory{
		services: p.Opts.Services,
		logger:   p.Logger,
		clk:      realClock(p.Clk),
		cache:    store,
		newHTTP:  client.New,
	}
	p.Logger.Debug(
		"goenvoy: started",
		slog.Int("services", len(p.Opts.Services)),
		slog.Bool("cache", p.Cache != nil),
	)
	return newRegistryFromFactory(f)
}

func newRegistryFromFactory(f *factory) *Registry {
	return &Registry{
		f:       f,
		sonarr:  make(map[string]*sonarr.Client),
		tmdb:    make(map[string]*tmdb.Client),
		anilist: make(map[string]*anilist.Client),
		trakt:   make(map[string]*trakt.Client),
	}
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
	f := &factory{
		services: opts.Services,
		logger:   logger,
		clk:      realClock(clk),
		cache:    cache,
		newHTTP:  newHTTP,
	}
	return newRegistryFromFactory(f)
}
