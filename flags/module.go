package flags

import (
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Module provides a [Provider] and a *[Client] to the fx graph. The provider
// backend is selected by config; the default "noop" backend is a safe null
// object that always returns the caller's default value.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    flags.Module, // provides flags.Provider + *flags.Client
//	    fx.Invoke(func(c *flags.Client) {
//	        if c.Bool(ctx, "dark-mode", false) { ... }
//	    }),
//	)
//
// Config key prefix: flags.* — e.g. flags.backend.
var Module = fx.Module("golusoris.flags",
	fx.Provide(loadOptions),
	fx.Provide(newProvider),
	fx.Provide(New),
)

// Options selects and tunes the feature-flag provider.
type Options struct {
	// Backend selects the provider implementation: "noop" (default, safe null)
	// or "memory" (in-process, for tests and local development).
	Backend string `koanf:"backend"`
}

func defaultOptions() Options {
	return Options{Backend: "noop"}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("flags", &opts); err != nil {
		return Options{}, fmt.Errorf("flags: load options: %w", err)
	}
	return opts, nil
}

func newProvider(opts Options, logger *slog.Logger) (Provider, error) {
	var p Provider
	switch opts.Backend {
	case "", "noop":
		p = NoopProvider{}
	case "memory":
		p = NewMemoryProvider()
	default:
		return nil, fmt.Errorf("flags: unknown backend %q", opts.Backend)
	}
	logger.Debug("flags: started", slog.String("backend", p.Metadata().Name))
	return p, nil
}
