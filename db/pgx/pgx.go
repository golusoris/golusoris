// Package pgx wires a [*pgxpool.Pool] as an fx dependency. The module reads
// its configuration from [config.Config] under the "db" key, retries the
// initial connection with exponential backoff (so apps don't crash-loop while
// waiting for the database sidecar), and optionally logs slow queries.
//
// Apps compose it via golusoris.DB or import the Module directly:
//
//	fx.New(
//	    golusoris.Core,
//	    pgx.Module,
//	)
//
// Config keys (env prefix defaults to APP_ → APP_DB_DSN, APP_DB_POOL_MAX, ...):
//
//	db.dsn                  # required, pgx DSN
//	db.pool.min             # min pool size (default 0)
//	db.pool.max             # max pool size (default 10)
//	db.pool.lifetime        # max connection lifetime (default 1h)
//	db.pool.idle            # max connection idle time (default 30m)
//	db.pool.healthcheck     # healthcheck period (default 1m)
//	db.connect_timeout      # single-attempt connect timeout (default 5s)
//	db.retry.attempts       # max connect attempts on start (default 10)
//	db.retry.initial        # initial backoff delay (default 50ms)
//	db.retry.max            # max backoff delay (default 5s)
//	db.tracing.slow         # slow-query log threshold, 0 disables (default 200ms)
package pgx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
)

// Options configures the pgx pool. Zero value is mostly usable after
// [Options.withDefaults] fills in sane defaults, except DSN must be set.
type Options struct {
	DSN            string         `koanf:"dsn"`
	Pool           PoolOptions    `koanf:"pool"`
	ConnectTimeout time.Duration  `koanf:"connect_timeout"`
	Retry          RetryOptions   `koanf:"retry"`
	Tracing        TracingOptions `koanf:"tracing"`
}

// PoolOptions maps to the corresponding fields on [pgxpool.Config].
type PoolOptions struct {
	Min         int32         `koanf:"min"`
	Max         int32         `koanf:"max"`
	Lifetime    time.Duration `koanf:"lifetime"`
	Idle        time.Duration `koanf:"idle"`
	Healthcheck time.Duration `koanf:"healthcheck"`
}

// RetryOptions tunes the on-start connect retry. Exponential backoff:
// Initial, Initial*2, Initial*4, ... capped at Max, up to Attempts tries.
type RetryOptions struct {
	Attempts int           `koanf:"attempts"`
	Initial  time.Duration `koanf:"initial"`
	Max      time.Duration `koanf:"max"`
}

// TracingOptions configures the slow-query logger. Set Slow to 0 to disable.
type TracingOptions struct {
	Slow time.Duration `koanf:"slow"`
}

// DefaultOptions returns the opinionated defaults. DSN is still required.
func DefaultOptions() Options {
	return Options{
		Pool: PoolOptions{
			Min:         0,
			Max:         10,
			Lifetime:    time.Hour,
			Idle:        30 * time.Minute,
			Healthcheck: time.Minute,
		},
		ConnectTimeout: 5 * time.Second,
		Retry: RetryOptions{
			Attempts: 10,
			Initial:  50 * time.Millisecond,
			Max:      5 * time.Second,
		},
		Tracing: TracingOptions{Slow: 200 * time.Millisecond},
	}
}

// withDefaults fills zero-valued non-DSN fields from [DefaultOptions].
func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Pool.Max == 0 {
		o.Pool.Max = d.Pool.Max
	}
	if o.Pool.Lifetime == 0 {
		o.Pool.Lifetime = d.Pool.Lifetime
	}
	if o.Pool.Idle == 0 {
		o.Pool.Idle = d.Pool.Idle
	}
	if o.Pool.Healthcheck == 0 {
		o.Pool.Healthcheck = d.Pool.Healthcheck
	}
	if o.ConnectTimeout == 0 {
		o.ConnectTimeout = d.ConnectTimeout
	}
	if o.Retry.Attempts == 0 {
		o.Retry.Attempts = d.Retry.Attempts
	}
	if o.Retry.Initial == 0 {
		o.Retry.Initial = d.Retry.Initial
	}
	if o.Retry.Max == 0 {
		o.Retry.Max = d.Retry.Max
	}
	// Tracing.Slow == 0 means "disabled" — don't override.
	return o
}

// loadOptions unmarshals the "db" key on top of [DefaultOptions]. Callers may
// override any field by supplying their own [Options] provider before the
// Module, via fx.Replace or fx.Decorate.
func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("db", &opts); err != nil {
		return Options{}, fmt.Errorf("db/pgx: load options: %w", err)
	}
	opts = opts.withDefaults()
	if opts.DSN == "" {
		return Options{}, errMissingDSN
	}
	return opts, nil
}

// New constructs a connected [*pgxpool.Pool] honoring the retry policy. The
// returned pool is ready for use. Callers must Close it. Prefer the fx
// [Module] in application code.
func New(ctx context.Context, opts Options, logger *slog.Logger, clk clock.Clock) (*pgxpool.Pool, error) {
	opts = opts.withDefaults()
	if opts.DSN == "" {
		return nil, errMissingDSN
	}

	cfg, err := pgxpool.ParseConfig(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("db/pgx: parse DSN: %w", err)
	}
	cfg.MinConns = opts.Pool.Min
	cfg.MaxConns = opts.Pool.Max
	cfg.MaxConnLifetime = opts.Pool.Lifetime
	cfg.MaxConnIdleTime = opts.Pool.Idle
	cfg.HealthCheckPeriod = opts.Pool.Healthcheck

	if opts.Tracing.Slow > 0 {
		cfg.ConnConfig.Tracer = newSlowQueryTracer(logger, opts.Tracing.Slow, clk)
	}

	pool, err := connectWithRetry(ctx, cfg, opts, logger, clk)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// connectWithRetry establishes the initial pool + validates via Ping, retrying
// up to opts.Retry.Attempts with exponential backoff. ctx cancellation is
// honored between attempts.
func connectWithRetry(
	ctx context.Context,
	cfg *pgxpool.Config,
	opts Options,
	logger *slog.Logger,
	clk clock.Clock,
) (*pgxpool.Pool, error) {
	delay := opts.Retry.Initial
	var lastErr error
	for attempt := 1; attempt <= opts.Retry.Attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
		pool, err := pgxpool.NewWithConfig(attemptCtx, cfg)
		if err == nil {
			err = pool.Ping(attemptCtx)
			if err == nil {
				cancel()
				logger.Info("db/pgx: connected", slog.Int("attempt", attempt))
				return pool, nil
			}
			pool.Close()
		}
		cancel()
		lastErr = err
		if attempt == opts.Retry.Attempts {
			break
		}
		logger.Warn("db/pgx: connect failed, will retry",
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", opts.Retry.Attempts),
			slog.Duration("next_delay", delay),
			slog.String("error", err.Error()),
		)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("db/pgx: connect canceled: %w", ctx.Err())
		case <-clk.After(delay):
		}
		delay *= 2
		if delay > opts.Retry.Max {
			delay = opts.Retry.Max
		}
	}
	return nil, fmt.Errorf("db/pgx: connect failed after %d attempts: %w", opts.Retry.Attempts, lastErr)
}

// Module provides a [*pgxpool.Pool] built from config.Config["db"], with
// retry-on-start and lifecycle-managed shutdown. Requires [config.Module],
// [log.Module], and [clock.Module] in the same fx graph (all included in
// [golusoris.Core]).
var Module = fx.Module("golusoris.db.pgx",
	fx.Provide(loadOptions),
	fx.Provide(
		func(lc fx.Lifecycle, opts Options, logger *slog.Logger, clk clock.Clock) (*pgxpool.Pool, error) {
			// Background context: fx.Hook OnStart ctx can expire; we want
			// the pool to outlive it. Start-attempt ctxs are scoped per attempt.
			pool, err := New(context.Background(), opts, logger, clk)
			if err != nil {
				return nil, err
			}
			lc.Append(fx.Hook{
				OnStop: func(_ context.Context) error {
					pool.Close()
					return nil
				},
			})
			return pool, nil
		},
	),
)
