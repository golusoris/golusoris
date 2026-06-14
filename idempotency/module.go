// This file wires the idempotency package as an opt-in golusoris fx module.
//
// The module provides the [Store] interface (defaulting to an in-memory,
// clock-injected [MemoryStore]) plus a configured [middleware.Middleware]
// built from [Config]. Apps swap in a Redis/Postgres-backed store with
// fx.Decorate over [Store]:
//
//	fx.New(
//	    golusoris.Core,
//	    idempotency.Module,            // provides idempotency.Store + middleware.Middleware
//	    fx.Decorate(func(idempotency.Store) idempotency.Store {
//	        return myRedisStore        // production-grade, shared across replicas
//	    }),
//	    fx.Invoke(func(mw middleware.Middleware) { mux.Use(mw) }),
//	)
//
// Config key prefix: idempotency.* (env: APP_IDEMPOTENCY_*)
//
//	idempotency.required  # reject requests without the header (default false)
//	idempotency.ttl       # how long a cached response is retained (default 24h)
//	idempotency.header    # request header carrying the key (default Idempotency-Key)

package idempotency

import (
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Config tunes the idempotency module from configuration. It mirrors the
// fields of [Options]; the existing [Options] type carries no koanf tags, so
// the module keeps its own tagged struct and maps it at construction time.
type Config struct {
	// Required, when true, rejects requests without the header (HTTP 400).
	Required bool `koanf:"required"`
	// TTL is how long a cached response is retained (default 24h).
	TTL time.Duration `koanf:"ttl"`
	// Header is the request header carrying the idempotency key
	// (default "Idempotency-Key").
	Header string `koanf:"header"`
}

func defaultOptions() Config {
	return Config{
		Required: false,
		TTL:      24 * time.Hour,
		Header:   "Idempotency-Key",
	}
}

func loadOptions(cfg *config.Config) (Config, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("idempotency", &opts); err != nil {
		return Config{}, fmt.Errorf("idempotency: load options: %w", err)
	}
	return opts, nil
}

// newStore builds the default in-memory [Store]. It is provided as the [Store]
// interface so apps replace it with fx.Decorate without touching this module.
func newStore(clk clock.Clock, logger *slog.Logger) Store {
	logger.Debug("idempotency: using in-memory store (not shared across replicas)")
	return NewMemoryStoreWithClock(clk)
}

// newMiddleware builds the configured idempotency middleware over the
// fx-provided (possibly decorated) [Store].
func newMiddleware(store Store, cfg Config) middleware.Middleware {
	return Middleware(store, Options{
		Required: cfg.Required,
		TTL:      cfg.TTL,
		Header:   cfg.Header,
	})
}

// Module provides idempotency.Store (default in-memory) and a configured
// httpx/middleware.Middleware to the fx graph.
var Module = fx.Module("golusoris.idempotency",
	fx.Provide(loadOptions),
	fx.Provide(newStore),
	fx.Provide(newMiddleware),
)
