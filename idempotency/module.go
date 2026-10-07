// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// This file wires the idempotency package as an opt-in golusoris fx module.
//
// The module provides the [Store] interface, selected by idempotency.store,
// plus a configured [middleware.Middleware] built from [Config]. Shared
// backends take their client from the graph: postgres needs *pgxpool.Pool
// (db/pgx) with [MigrationsFS] applied, redis needs rueidis.Client
// (cache/redis), sqlite needs *sql.DB (db/sqlite). Apps may still replace the
// Store with fx.Decorate.
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.DB,                  // *pgxpool.Pool for idempotency.store=postgres
//	    idempotency.Module,            // provides idempotency.Store + middleware.Middleware
//	    fx.Invoke(func(mw middleware.Middleware) { mux.Use(mw) }),
//	)
//
// Config key prefix: idempotency.* (env: APP_IDEMPOTENCY_*)
//
//	idempotency.required  # reject requests without the header (default false)
//	idempotency.ttl       # how long a cached response is retained (default 24h)
//	idempotency.header    # request header carrying the key (default Idempotency-Key)
//	idempotency.max_request_body   # fingerprint buffer bound (default 1 MiB)
//	idempotency.max_response_body  # replay capture bound (default 1 MiB)
//	idempotency.store              # memory (default) | postgres | redis | sqlite
//	idempotency.redis.prefix       # redis key prefix (default golusoris:idempotency:)
//	idempotency.sweep.interval     # expired-key GC period, 0 disables (default 1m)
//	idempotency.sweep.batch        # rows per sweep batch, max 10000 (default 1000)
//	idempotency.sweep.timeout      # bound per sweep batch (default 10s)

package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Store backends selectable by idempotency.store.
const (
	StoreMemory   = "memory"
	StorePostgres = "postgres"
	StoreRedis    = "redis"
	StoreSQLite   = "sqlite"
)

const (
	// maxSweepBatchesPerTick bounds the catch-up work one sweep tick performs.
	maxSweepBatchesPerTick = 16
	// sqliteSchemaTimeout bounds the CREATE TABLE IF NOT EXISTS at start.
	sqliteSchemaTimeout = 10 * time.Second
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
	// MaxRequestBody bounds request bytes buffered for fingerprinting.
	MaxRequestBody int64 `koanf:"max_request_body"`
	// MaxResponseBody bounds response bytes retained for replay.
	MaxResponseBody int64 `koanf:"max_response_body"`
	// Store selects the backend: memory (default), postgres, redis, sqlite.
	Store string `koanf:"store"`
	// Redis tunes the redis backend.
	Redis RedisConfig `koanf:"redis"`
	// Sweep tunes expired-key garbage collection for memory, postgres and sqlite.
	Sweep SweepConfig `koanf:"sweep"`
}

// RedisConfig tunes [RedisStore] under idempotency.redis.*.
type RedisConfig struct {
	// Prefix namespaces keys (default [DefaultRedisPrefix]).
	Prefix string `koanf:"prefix"`
}

// SweepConfig tunes the expired-key sweeper under idempotency.sweep.*.
type SweepConfig struct {
	// Interval is the sweep period; 0 disables the sweeper (default 1m).
	Interval time.Duration `koanf:"interval"`
	// Batch caps rows deleted per statement, at most [MaxSweepBatch] (default 1000).
	Batch int `koanf:"batch"`
	// Timeout bounds each sweep batch (default 10s).
	Timeout time.Duration `koanf:"timeout"`
}

func defaultOptions() Config {
	return Config{
		Required:        false,
		TTL:             24 * time.Hour,
		Header:          "Idempotency-Key",
		MaxRequestBody:  defaultBodyLimit,
		MaxResponseBody: defaultBodyLimit,
		Store:           StoreMemory,
		Redis:           RedisConfig{Prefix: DefaultRedisPrefix},
		Sweep:           SweepConfig{Interval: time.Minute, Batch: 1000, Timeout: 10 * time.Second},
	}
}

func loadOptions(cfg *config.Config) (Config, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("idempotency", &opts); err != nil {
		return Config{}, fmt.Errorf("idempotency: load options: %w", err)
	}
	if err := opts.Sweep.validate(); err != nil {
		return Config{}, err
	}
	return opts, nil
}

func (s SweepConfig) validate() error {
	if s.Interval < 0 || s.Timeout < 0 {
		return errors.New("idempotency: sweep interval and timeout must not be negative")
	}
	if s.Interval > 0 && (s.Batch <= 0 || s.Batch > MaxSweepBatch || s.Timeout == 0) {
		return fmt.Errorf("idempotency: sweep needs batch in 1..%d and a positive timeout", MaxSweepBatch)
	}
	return nil
}

// storeParams carries the optional backend clients; only the selected one is required.
type storeParams struct {
	fx.In

	Config Config
	Clock  clock.Clock
	Logger *slog.Logger
	Pool   *pgxpool.Pool  `optional:"true"`
	Redis  rueidis.Client `optional:"true"`
	DB     *sql.DB        `optional:"true"`
}

// newStore builds the [Store] named by idempotency.store. It is provided as
// the interface so apps can still replace it with fx.Decorate.
func newStore(p storeParams) (Store, error) {
	switch p.Config.Store {
	case StoreMemory, "":
		p.Logger.Debug("idempotency: using in-memory store (not shared across replicas)")
		return NewMemoryStoreWithClock(p.Clock), nil
	case StorePostgres:
		if p.Pool == nil {
			return nil, errors.New("idempotency: store postgres needs a *pgxpool.Pool (db/pgx)")
		}
		return NewPostgresStore(p.Pool, p.Clock)
	case StoreRedis:
		if validate.IsNil(p.Redis) {
			return nil, errors.New("idempotency: store redis needs a rueidis.Client (cache/redis)")
		}
		return NewRedisStore(p.Redis, p.Config.Redis.Prefix)
	case StoreSQLite:
		if p.DB == nil {
			return nil, errors.New("idempotency: store sqlite needs a *sql.DB (db/sqlite)")
		}
		return newModuleSQLiteStore(p.DB, p.Clock)
	default:
		return nil, fmt.Errorf("idempotency: unknown store %q", p.Config.Store)
	}
}

func newModuleSQLiteStore(db *sql.DB, clk clock.Clock) (Store, error) {
	store, err := NewSQLiteStore(db, clk)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sqliteSchemaTimeout)
	defer cancel()
	if err := store.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// newMiddleware builds the configured idempotency middleware over the
// fx-provided (possibly decorated) [Store].
func newMiddleware(store Store, cfg Config, logger *slog.Logger) middleware.Middleware {
	return Middleware(store, Options{
		Required:        cfg.Required,
		TTL:             cfg.TTL,
		Header:          cfg.Header,
		MaxRequestBody:  cfg.MaxRequestBody,
		MaxResponseBody: cfg.MaxResponseBody,
		Logger:          logger,
	})
}

// registerSweeper runs the bounded sweep loop when the store implements [Sweeper].
func registerSweeper(lc fx.Lifecycle, store Store, cfg Config, clk clock.Clock, logger *slog.Logger) {
	sweeper, ok := store.(Sweeper)
	if !ok || cfg.Sweep.Interval == 0 {
		return
	}
	loop := sweepLoop{sweeper: sweeper, cfg: cfg.Sweep, clk: clk, logger: logger}
	stop := make(chan struct{})
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() { //nolint:contextcheck // the loop outlives fx's start context; each batch derives its own deadline
				defer close(done)
				loop.run(stop)
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			close(stop)
			select {
			case <-done:
				return nil
			case <-stopCtx.Done():
				return fmt.Errorf("idempotency: stop sweeper: %w", stopCtx.Err())
			}
		},
	})
}

// sweepLoop deletes expired records every interval until stop closes. Each
// batch carries its own deadline, so shutdown waits at most one batch timeout.
type sweepLoop struct {
	sweeper Sweeper
	cfg     SweepConfig
	clk     clock.Clock
	logger  *slog.Logger
}

func (l sweepLoop) run(stop <-chan struct{}) {
	for l.wait(stop) {
		l.tick(stop)
	}
}

// wait reports whether the next interval elapsed before stop closed.
func (l sweepLoop) wait(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return false
	case <-l.clk.After(l.cfg.Interval):
		return true
	}
}

// tick sweeps full batches until one comes back short, stop closes, or the
// per-tick cap hits.
func (l sweepLoop) tick(stop <-chan struct{}) int64 {
	var total int64
	for range maxSweepBatchesPerTick {
		removed, err := l.batch()
		total += removed
		if err != nil {
			l.logger.Warn("idempotency: sweep expired keys", slog.Any("err", err))
			return total
		}
		if removed < int64(l.cfg.Batch) || stopped(stop) {
			return total
		}
	}
	return total
}

func (l sweepLoop) batch() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), l.cfg.Timeout)
	defer cancel()
	removed, err := l.sweeper.Sweep(ctx, l.cfg.Batch)
	if err != nil {
		return 0, fmt.Errorf("idempotency: sweep: %w", err)
	}
	return removed, nil
}

func stopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// Module provides idempotency.Store (selected by idempotency.store) and a
// configured httpx/middleware.Middleware to the fx graph, and sweeps expired
// keys for stores that implement [Sweeper].
var Module = fx.Module(
	"golusoris.idempotency",
	fx.Provide(loadOptions),
	fx.Provide(newStore),
	fx.Provide(newMiddleware),
	fx.Invoke(registerSweeper),
)
