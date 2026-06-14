// This file wires extclient as an opt-in golusoris fx module.
//
// The module reads named upstream services from config (prefix
// httpx.extclient.services.<name>.*) and provides a [*Registry] that hands out
// a configured [*Client] per name. Apps look one up in a constructor:
//
//	fx.New(
//	    golusoris.Core,
//	    memory.Module,        // optional: enables response caching
//	    extclient.Module,     // provides *extclient.Registry
//	    fx.Invoke(func(r *extclient.Registry) error {
//	        github, err := r.Client("github")
//	        ...
//	    }),
//	)
//
// Config (env: APP_HTTPX_EXTCLIENT_*):
//
//	httpx.extclient.services.github.base_url   = "https://api.github.com"
//	httpx.extclient.services.github.bearer     = "${GH_TOKEN}"
//	httpx.extclient.services.github.headers.accept = "application/vnd.github+json"
//	httpx.extclient.services.github.timeout    = "10s"
//	httpx.extclient.services.github.cache_ttl  = "30s"
//	httpx.extclient.services.github.retry.max  = 3
//	httpx.extclient.services.github.breaker.max = 5

package extclient

import (
	"fmt"
	"log/slog"
	"sort"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/config"
)

// Options is the koanf-bound config under the httpx.extclient prefix. Each
// entry in Services configures one named upstream [Client].
type Options struct {
	// Services maps a logical service name to its per-host options.
	Services map[string]ServiceOptions `koanf:"services"`
}

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("httpx.extclient", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/extclient: load options: %w", err)
	}
	return opts, nil
}

// Registry holds the clients built from config, keyed by service name.
type Registry struct {
	clients map[string]*Client
}

// Client returns the client registered under name, or an error naming the
// services that are configured.
func (r *Registry) Client(name string) (*Client, error) {
	c, ok := r.clients[name]
	if !ok {
		return nil, fmt.Errorf("extclient: no service %q configured (have: %v)", name, r.names())
	}
	return c, nil
}

// Names returns the configured service names in sorted order.
func (r *Registry) Names() []string { return r.names() }

func (r *Registry) names() []string {
	out := make([]string, 0, len(r.clients))
	for n := range r.clients {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// registryParams are the fx inputs for [newRegistry]. The cache is optional —
// fx supplies nil when no memory.Module is wired, disabling response caching.
type registryParams struct {
	fx.In

	Opts   Options
	Logger *slog.Logger
	Cache  *memory.Cache `optional:"true"`
}

// newRegistry builds one [Client] per configured service.
func newRegistry(p registryParams) (*Registry, error) {
	clients := make(map[string]*Client, len(p.Opts.Services))
	for name, svc := range p.Opts.Services {
		if svc.Name == "" {
			svc.Name = name
		}
		options := []Option{WithLogger(p.Logger)}
		if p.Cache != nil {
			options = append(options, WithCache(p.Cache))
		}
		c, err := New(svc, options...)
		if err != nil {
			return nil, fmt.Errorf("extclient: build service %q: %w", name, err)
		}
		clients[name] = c
	}
	p.Logger.Debug("httpx/extclient: started",
		slog.Int("services", len(clients)),
		slog.Bool("cache", p.Cache != nil),
	)
	return &Registry{clients: clients}, nil
}

// Module provides a *Registry to the fx graph.
var Module = fx.Module("golusoris.httpx.extclient",
	fx.Provide(loadOptions),
	fx.Provide(newRegistry),
)

// NewRegistryForTest builds a Registry from explicit options without fx, for
// tests. cache may be nil to disable caching.
func NewRegistryForTest(opts Options, logger *slog.Logger, cache *memory.Cache) (*Registry, error) {
	return newRegistry(registryParams{Opts: opts, Logger: logger, Cache: cache})
}
