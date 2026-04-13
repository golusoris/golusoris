// Package profiling wires grafana/pyroscope-go for continuous in-process
// profiling. Off by default — enable via config. eBPF-mode profiling (node-
// wide) ships as deploy manifests in [deploy/observability/], not in this
// package.
//
// Config keys (env: APP_PROFILING_*):
//
//	profiling.enabled   # master switch (default false)
//	profiling.server    # Pyroscope server URL (default http://pyroscope:4040)
//	profiling.app       # application name (default = otel.service.name if set, else "app")
//	profiling.user      # basic-auth user (optional)
//	profiling.password  # basic-auth password (optional)
//	profiling.tenant    # Phlare multi-tenancy (optional)
package profiling

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/grafana/pyroscope-go"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options tunes the Pyroscope client.
type Options struct {
	Enabled  bool   `koanf:"enabled"`
	Server   string `koanf:"server"`
	App      string `koanf:"app"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Tenant   string `koanf:"tenant"`
}

// DefaultOptions returns disabled, server=http://pyroscope:4040.
func DefaultOptions() Options {
	return Options{
		Server: "http://pyroscope:4040",
	}
}

// Start launches the profiler. Nil-safe: when opts.Enabled=false returns
// (nil, nil) so callers don't branch.
func Start(opts Options) (*pyroscope.Profiler, error) {
	if !opts.Enabled {
		return nil, nil //nolint:nilnil // documented nil-nil no-op contract
	}
	if opts.App == "" {
		opts.App = "app"
	}
	p, err := pyroscope.Start(pyroscope.Config{
		ApplicationName:   opts.App,
		ServerAddress:     opts.Server,
		BasicAuthUser:     opts.User,
		BasicAuthPassword: opts.Password,
		TenantID:          opts.Tenant,
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("profiling: start: %w", err)
	}
	return p, nil
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("profiling", &opts); err != nil {
		return Options{}, fmt.Errorf("profiling: load options: %w", err)
	}
	return opts, nil
}

// Module starts the profiler during fx Start (when enabled) and stops it
// during fx Stop. No-op when profiling.enabled=false.
var Module = fx.Module("golusoris.observability.profiling",
	fx.Provide(loadOptions),
	fx.Invoke(func(lc fx.Lifecycle, opts Options, logger *slog.Logger) error {
		profiler, err := Start(opts)
		if err != nil {
			return err
		}
		if profiler != nil {
			logger.Info("profiling: started",
				slog.String("app", opts.App),
				slog.String("server", opts.Server),
			)
		}
		lc.Append(fx.Hook{
			OnStop: func(_ context.Context) error {
				if profiler == nil {
					return nil
				}
				if stopErr := profiler.Stop(); stopErr != nil {
					return fmt.Errorf("profiling: stop: %w", stopErr)
				}
				return nil
			},
		})
		return nil
	}),
)
