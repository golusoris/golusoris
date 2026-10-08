// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
//	db.password_file        # role password file, re-read per new connection
//	db.read_dsn             # optional read-only DSN → *ReadPool (else primary)
//	db.ssl.mode             # libpq sslmode, overrides the DSN
//	db.ssl.rootcert         # CA file (sslrootcert), re-read per new connection
//	db.ssl.cert / db.ssl.key  # client certificate + key files (sslcert/sslkey)
package pgx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
)

// Options configures the pgx pool. Zero value is mostly usable after
// [Options.withDefaults] fills in sane defaults, except DSN must be set.
type Options struct {
	DSN string `koanf:"dsn"`
	// PasswordFile holds the role password (CloudNativePG secret key
	// "password"). It overrides a DSN password and is re-read before every
	// new connection, so a rotated secret applies without a restart.
	PasswordFile string `koanf:"password_file"`
	// ReadDSN, when set, backs [ReadPool] with its own read-only pool
	// (CloudNativePG "-ro" service); every other option is shared.
	ReadDSN        string         `koanf:"read_dsn"`
	SSL            SSLOptions     `koanf:"ssl"`
	Pool           PoolOptions    `koanf:"pool"`
	ConnectTimeout time.Duration  `koanf:"connect_timeout"`
	Retry          RetryOptions   `koanf:"retry"`
	Tracing        TracingOptions `koanf:"tracing"`
	// Tracers are app-supplied pgx.QueryTracers (e.g. per-query metrics),
	// composed alongside the built-in slow-query tracer. Code-supplied only
	// (via fx.Decorate on Options) — never from config.
	Tracers []pgx.QueryTracer `koanf:"-"`
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
	if err := validateOptions(opts); err != nil {
		return Options{}, err
	}
	return opts, nil
}

func validateOptions(opts Options) error {
	if err := validatePoolOptions(opts.Pool); err != nil {
		return err
	}
	if opts.ConnectTimeout <= 0 {
		return errors.New("db/pgx: connect timeout must be positive")
	}
	if opts.Tracing.Slow < 0 {
		return errors.New("db/pgx: slow-query threshold must not be negative")
	}
	return validateRetryOptions(opts.Retry)
}

func validatePoolOptions(opts PoolOptions) error {
	if opts.Min < 0 || opts.Max <= 0 {
		return errors.New("db/pgx: pool minimum must be nonnegative and maximum positive")
	}
	if opts.Min > opts.Max {
		return errors.New("db/pgx: pool minimum exceeds maximum")
	}
	if opts.Lifetime < 0 || opts.Idle < 0 || opts.Healthcheck < 0 {
		return errors.New("db/pgx: pool durations must not be negative")
	}
	return nil
}

func validateRetryOptions(opts RetryOptions) error {
	if opts.Attempts <= 0 {
		return errors.New("db/pgx: retry attempts must be positive")
	}
	if opts.Initial <= 0 || opts.Max <= 0 {
		return errors.New("db/pgx: retry delays must be positive")
	}
	if opts.Initial > opts.Max {
		return errors.New("db/pgx: initial retry delay exceeds maximum")
	}
	return nil
}

// composeTracer combines the optional built-in slow-query tracer with any
// app-supplied tracers into one pgx.QueryTracer (nil when there are none,
// the single tracer when there is one, else a multitracer).
func composeTracer(slow pgx.QueryTracer, custom []pgx.QueryTracer) pgx.QueryTracer {
	tracers := make([]pgx.QueryTracer, 0, len(custom)+1)
	if !validate.IsNil(slow) {
		tracers = append(tracers, slow)
	}
	for _, tracer := range custom {
		if !validate.IsNil(tracer) {
			tracers = append(tracers, tracer)
		}
	}
	switch len(tracers) {
	case 0:
		return nil
	case 1:
		return tracers[0]
	default:
		return multitracer.New(tracers...)
	}
}

// New constructs a connected [*pgxpool.Pool] honoring the retry policy. The
// returned pool is ready for use. Callers must Close it. Prefer the fx
// [Module] in application code.
func New(ctx context.Context, opts Options, logger *slog.Logger, clk clock.Clock) (*pgxpool.Pool, error) {
	return newPool(ctx, opts, logger, clk, false)
}

func newPool(ctx context.Context, opts Options, logger *slog.Logger, clk clock.Clock, readOnly bool) (*pgxpool.Pool, error) {
	opts = opts.withDefaults()
	if opts.DSN == "" {
		return nil, errMissingDSN
	}
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if logger == nil {
		return nil, errors.New("db/pgx: nil logger")
	}
	if validate.IsNil(clk) {
		return nil, errors.New("db/pgx: nil clock")
	}
	cfg, err := poolConfig(ctx, opts, logger, clk)
	if err != nil {
		return nil, err
	}
	if readOnly {
		// Guards a "-r" (any instance) service too; standbys reject writes anyway.
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	return connectWithRetry(ctx, cfg, opts, logger, clk)
}

// poolConfig parses the DSN with SSL file options appended and applies pool
// sizing, tracers, and secret refresh.
func poolConfig(ctx context.Context, opts Options, logger *slog.Logger, clk clock.Clock) (*pgxpool.Config, error) {
	dsn := withConnParams(opts.DSN, opts.SSL.params())
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db/pgx: parse DSN: %w", err)
	}
	cfg.MinConns = opts.Pool.Min
	cfg.MaxConns = opts.Pool.Max
	cfg.MaxConnLifetime = opts.Pool.Lifetime
	cfg.MaxConnIdleTime = opts.Pool.Idle
	cfg.HealthCheckPeriod = opts.Pool.Healthcheck

	var slow pgx.QueryTracer
	if opts.Tracing.Slow > 0 {
		slow = newSlowQueryTracer(logger, opts.Tracing.Slow, clk)
	}
	if tracer := composeTracer(slow, opts.Tracers); tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	}
	if err = applySecrets(ctx, cfg, dsn, opts); err != nil {
		return nil, err
	}
	return cfg, nil
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
				logger.InfoContext(ctx, "db/pgx: connected", slog.Int("attempt", attempt))
				return pool, nil
			}
			pool.Close()
		}
		cancel()
		lastErr = err
		if attempt == opts.Retry.Attempts {
			break
		}
		logger.WarnContext(
			ctx, "db/pgx: connect failed, will retry",
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
		delay = nextRetryDelay(delay, opts.Retry.Max)
	}
	return nil, fmt.Errorf("db/pgx: connect failed after %d attempts: %w", opts.Retry.Attempts, lastErr)
}

// connectBudget bounds the whole start-up connect: every attempt's timeout
// plus the longest backoff after it, saturating instead of overflowing.
func (o Options) connectBudget() time.Duration {
	o = o.withDefaults()
	perAttempt := o.ConnectTimeout + o.Retry.Max
	if perAttempt <= 0 || int64(o.Retry.Attempts) > math.MaxInt64/int64(perAttempt) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(o.Retry.Attempts) * perAttempt
}

func nextRetryDelay(delay, maximum time.Duration) time.Duration {
	if delay >= maximum || delay > maximum/2 {
		return maximum
	}
	return delay * 2
}

// Module provides a [*pgxpool.Pool] built from config.Config["db"], with
// retry-on-start and lifecycle-managed shutdown, plus a [*ReadPool] that is
// built only when something depends on it. Requires [config.Module],
// [log.Module], and [clock.Module] in the same fx graph (all included in
// [golusoris.Core]).
var Module = fx.Module(
	"golusoris.db.pgx",
	fx.Provide(loadOptions),
	fx.Provide(
		func(lc fx.Lifecycle, opts Options, logger *slog.Logger, clk clock.Clock) (*pgxpool.Pool, error) {
			// Not the fx start ctx: the pool outlives it. The budget covers
			// every attempt plus backoff; attempt ctxs are scoped per try.
			startCtx, cancel := context.WithTimeout(context.Background(), opts.connectBudget())
			defer cancel()
			pool, err := New(startCtx, opts, logger, clk)
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
	fx.Provide(provideReadPool),
)
