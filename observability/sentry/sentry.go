// Package sentry wires getsentry/sentry-go as a golusoris module with:
//
//   - slog-adapted handler: every slog.Error (and above) is also captured
//     as a Sentry event, carrying request_id + source location.
//   - Automatic flush on fx Stop so in-flight events aren't lost at
//     shutdown.
//
// Config keys (env: APP_SENTRY_*):
//
//	sentry.dsn                # required to enable; empty = no-op
//	sentry.environment        # e.g. "production"
//	sentry.release            # release identifier (git sha)
//	sentry.sample.rate        # event sample rate, 0-1 (default 1.0)
//	sentry.sample.traces      # trace sample rate, 0-1 (default 0)
//	sentry.debug              # print SDK debug info to stdout
//	sentry.flush.timeout      # grace period for Shutdown flush (default 5s)
package sentry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	sentrygo "github.com/getsentry/sentry-go"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options tunes the Sentry client.
type Options struct {
	DSN         string        `koanf:"dsn"`
	Environment string        `koanf:"environment"`
	Release     string        `koanf:"release"`
	Sample      SampleOptions `koanf:"sample"`
	Debug       bool          `koanf:"debug"`
	Flush       FlushOptions  `koanf:"flush"`
}

// SampleOptions tunes event + trace sampling.
type SampleOptions struct {
	Rate   float64 `koanf:"rate"`
	Traces float64 `koanf:"traces"`
}

// FlushOptions controls the Stop-phase drain.
type FlushOptions struct {
	Timeout time.Duration `koanf:"timeout"`
}

// DefaultOptions returns events at 100%, traces disabled, 5s flush.
func DefaultOptions() Options {
	return Options{
		Sample: SampleOptions{Rate: 1.0, Traces: 0},
		Flush:  FlushOptions{Timeout: 5 * time.Second},
	}
}

// Init initializes the global Sentry client. No-op when DSN is empty.
func Init(opts Options) error {
	if opts.DSN == "" {
		return nil
	}
	err := sentrygo.Init(sentrygo.ClientOptions{
		Dsn:              opts.DSN,
		Environment:      opts.Environment,
		Release:          opts.Release,
		SampleRate:       opts.Sample.Rate,
		EnableTracing:    opts.Sample.Traces > 0,
		TracesSampleRate: opts.Sample.Traces,
		Debug:            opts.Debug,
		AttachStacktrace: true,
	})
	if err != nil {
		return fmt.Errorf("sentry: init: %w", err)
	}
	return nil
}

// Flush waits for pending events to be sent, up to opts.Flush.Timeout.
// Returns true if everything flushed; false if the timeout expired first.
func Flush(timeout time.Duration) bool { return sentrygo.Flush(timeout) }

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("sentry", &opts); err != nil {
		return Options{}, fmt.Errorf("sentry: load options: %w", err)
	}
	return opts, nil
}

// Module initializes Sentry and installs the slog bridge handler. DSN-less
// configurations are a no-op — the bridge short-circuits.
var Module = fx.Module("golusoris.observability.sentry",
	fx.Provide(loadOptions),
	fx.Invoke(func(lc fx.Lifecycle, opts Options, existing *slog.Logger) error {
		if err := Init(opts); err != nil {
			return err
		}
		if opts.DSN != "" {
			// Fan out to the existing handler + Sentry bridge for Error+.
			bridged := slog.New(&fanoutHandler{
				handlers: []slog.Handler{existing.Handler(), newSentryHandler()},
			})
			slog.SetDefault(bridged)
		}
		lc.Append(fx.Hook{
			OnStop: func(_ context.Context) error {
				if opts.DSN == "" {
					return nil
				}
				Flush(opts.Flush.Timeout)
				return nil
			},
		})
		return nil
	}),
)
