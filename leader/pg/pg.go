// Package pg elects a single leader via a PostgreSQL session-scoped
// advisory lock. Works anywhere the app already has a pg connection
// (no k8s required). Suitable for Docker Compose, Swarm, Nomad, bare
// Linux, or k8s where the Lease API is unavailable.
//
// Mechanism:
//  1. Leader candidate dedicates a single connection from the pool and
//     calls pg_try_advisory_lock(key) until it wins.
//  2. While holding the lock the connection stays open — advisory
//     locks are released on session end (graceful close OR crash), so
//     there's no TTL + renewal dance.
//  3. On fx Stop the connection is closed, releasing the lock for the
//     next candidate immediately.
//
// Compared to the k8s Lease: simpler (no TTL tuning), fail-safe on
// crash (tcp keepalive detects dead sessions), but requires an
// always-available pg. Backend choice is per-app.
//
// Config keys (env: APP_LEADER_*):
//
//	leader.enabled  # master switch (default false)
//	leader.name     # human name used as the hash key (required)
//	leader.identity # this replica's identity (default hostname)
//	leader.pg.retry # how often to retry acquisition when held (default 2s)
package pg

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/leader"
)

// Options tunes the elector.
type Options struct {
	Enabled  bool           `koanf:"enabled"`
	Name     string         `koanf:"name"`
	Identity string         `koanf:"identity"`
	PG       BackendOptions `koanf:"pg"`
}

// BackendOptions groups backend-specific timing knobs.
type BackendOptions struct {
	Retry time.Duration `koanf:"retry"`
}

// DefaultOptions returns disabled + 2s retry.
func DefaultOptions() Options {
	return Options{PG: BackendOptions{Retry: 2 * time.Second}}
}

// keyFor hashes name into the int64 required by pg_advisory_lock.
// FNV-64a is fast + stable; collisions across different apps are
// harmless (they'd contend on the same lock, which is a caller-level
// config error — unique names per elector are the contract).
func keyFor(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64()) // #nosec G115 -- intentional bit-reuse for pg int8 advisory key
}

// Run blocks until ctx is canceled, running the pg-advisory-lock
// election loop. Never returns a "leadership lost" error — advisory
// locks disappear only on session close, and this function owns the
// session.
func Run(ctx context.Context, pool *pgxpool.Pool, opts Options, clk clock.Clock, cb leader.Callbacks) error {
	opts = opts.withDefaults()
	if opts.Name == "" {
		return fmt.Errorf("leader/pg: leader.name is required when enabled")
	}
	identity := opts.Identity
	if identity == "" {
		if h, err := os.Hostname(); err == nil {
			identity = h
		} else {
			identity = "unknown"
		}
	}
	key := keyFor(opts.Name)

	// Dedicate one connection so the advisory lock stays held.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("leader/pg: acquire conn: %w", err)
	}
	defer conn.Release()

	for {
		got, acqErr := tryLock(ctx, conn.Conn(), key)
		if acqErr != nil {
			return fmt.Errorf("leader/pg: try lock: %w", acqErr)
		}
		if got {
			if cb.OnNewLeader != nil {
				cb.OnNewLeader(identity)
			}
			leaderCtx, cancel := context.WithCancel(ctx)
			if cb.OnStartedLeading != nil {
				cb.OnStartedLeading(leaderCtx)
			}
			// Hold until ctx cancellation. The lock releases when conn
			// is released (deferred above).
			<-ctx.Done()
			cancel()
			if cb.OnStoppedLeading != nil {
				cb.OnStoppedLeading()
			}
			return nil
		}
		// Not leader: wait + retry.
		select {
		case <-ctx.Done():
			return nil
		case <-clk.After(opts.PG.Retry):
		}
	}
}

func tryLock(ctx context.Context, conn *pgx.Conn, key int64) (bool, error) {
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		return false, fmt.Errorf("leader/pg: try lock: %w", err)
	}
	return ok, nil
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.PG.Retry == 0 {
		o.PG.Retry = d.PG.Retry
	}
	return o
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("leader", &opts); err != nil {
		return Options{}, fmt.Errorf("leader/pg: load options: %w", err)
	}
	return opts, nil
}

// Module wires the pg-advisory-lock elector into fx. Requires a
// *pgxpool.Pool in the graph (from db/pgx). `leader.enabled=false`
// skips wiring entirely.
func Module(cb leader.Callbacks) fx.Option {
	return fx.Module("golusoris.leader.pg",
		fx.Provide(loadOptions),
		fx.Invoke(func(lc fx.Lifecycle, opts Options, pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger) {
			if !opts.Enabled {
				return
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			lc.Append(fx.Hook{
				OnStart: func(_ context.Context) error {
					go func() {
						defer close(done)
						if runErr := Run(ctx, pool, opts, clk, cb); runErr != nil {
							logger.Error("leader/pg: run failed", slog.String("error", runErr.Error()))
						}
					}()
					return nil
				},
				OnStop: func(_ context.Context) error {
					cancel()
					<-done
					return nil
				},
			})
		}),
	)
}
