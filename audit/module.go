package audit

// fx wiring for the audit log. Apps opt in with a single line:
//
//	fx.New(
//	    golusoris.Core, // provides config, log, clock
//	    audit.Module,   // provides *audit.Logger backed by a Store
//	)
//
// The default Store is an in-memory [MemoryStore]. Apps that need durable
// storage override it without touching this module:
//
//	fx.New(
//	    golusoris.Core,
//	    audit.Module,
//	    fx.Decorate(func(*audit.MemoryStore) audit.Store { return pgStore }),
//	)
//
// Config key prefix: audit.*

import (
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
)

// Options tunes the audit module.
type Options struct {
	// Enabled is reserved for apps that gate audit logging via config.
	// The module always provides a Logger; this flag is surfaced for
	// app-level conditionals and defaults to true.
	Enabled bool `koanf:"enabled"`
}

func defaultOptions() Options {
	return Options{Enabled: true}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("audit", &opts); err != nil {
		return Options{}, fmt.Errorf("audit: load options: %w", err)
	}
	return opts, nil
}

// newLogger builds a [Logger] from the injected Store and clock.
// The Store defaults to [MemoryStore] (see [Module]); apps override it
// via fx.Decorate to plug in durable storage.
func newLogger(_ Options, store Store, clk clock.Clock, logger *slog.Logger) *Logger {
	logger.Debug("audit: started")
	return New(store, WithClock(clk))
}

// Module provides *audit.Logger to the fx graph. The default Store is an
// in-memory [MemoryStore]; override it with fx.Decorate for durable storage.
var Module = fx.Module("golusoris.audit",
	fx.Provide(loadOptions),
	fx.Provide(func() Store { return NewMemoryStore() }), //nolint:gocritic // explicit return type aids fx
	fx.Provide(newLogger),
)
