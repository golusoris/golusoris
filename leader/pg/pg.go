// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/leader"
	"github.com/golusoris/golusoris/leader/internal/hook"
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
func keyFor(name string) (int64, error) {
	h := fnv.New64a()
	if _, err := h.Write([]byte(name)); err != nil {
		return 0, fmt.Errorf("leader/pg: hash name: %w", err)
	}
	return int64(h.Sum64()), nil // #nosec G115 -- intentional bit-reuse for pg int8 advisory key
}

// Run blocks until ctx is canceled, running the pg-advisory-lock
// election loop. Never returns a "leadership lost" error — advisory
// locks disappear only on session close, and this function owns the
// session.
func Run(ctx context.Context, pool *pgxpool.Pool, opts Options, clk clock.Clock, cb leader.Callbacks) error {
	opts = opts.withDefaults()
	if opts.Name == "" {
		return errors.New("leader/pg: leader.name is required when enabled")
	}
	identity := hook.Identity(opts.Identity)

	key, err := keyFor(opts.Name)
	if err != nil {
		return err
	}

	// Dedicate one connection so the advisory lock stays held.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("leader/pg: acquire conn: %w", err)
	}
	defer conn.Release()

	for ctx.Err() == nil {
		got, acqErr := tryLock(ctx, conn.Conn(), key)
		if acqErr != nil {
			return fmt.Errorf("leader/pg: try lock: %w", acqErr)
		}
		if got {
			// The lock is held until ctx cancellation and releases when
			// conn is released (deferred above).
			hook.Lead(ctx, identity, cb)
			return nil
		}
		// Not leader: wait + retry.
		select {
		case <-ctx.Done():
			return nil
		case <-clk.After(opts.PG.Retry):
		}
	}
	return nil
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

func loadOptions(cfg *config.Config) (Options, error) { return loadOptionsAt(cfg, "leader") }

func loadOptionsAt(cfg *config.Config, path string) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal(path, &opts); err != nil {
		return Options{}, fmt.Errorf("leader/pg: load options: %w", err)
	}
	return opts, nil
}

// Module wires the pg-advisory-lock elector into fx. Requires a
// *pgxpool.Pool in the graph (from db/pgx). `leader.enabled=false`
// skips wiring entirely.
func Module(cb leader.Callbacks) fx.Option {
	return fx.Module(
		"golusoris.leader.pg",
		fx.Provide(loadOptions),
		fx.Invoke(func(lc fx.Lifecycle, opts Options, pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger) {
			wire(lc, opts, pool, clk, logger, "leader/pg", cb)
		}),
	)
}

// NamedModule wires one more election, keyed by key, alongside [Module] or
// other NamedModules. Options load from leader.elections.<key> (same keys
// as leader.*; leader.elections.<key>.name must differ per election) and a
// *leader.Status tagged `name:"<key>"` reports its state. key must match
// [a-z][a-z0-9]{0,62}.
func NamedModule(key string, cb leader.Callbacks) fx.Option {
	status, tag, err := hook.NamedStatus(key)
	if err != nil {
		return fx.Error(fmt.Errorf("leader/pg: %w", err))
	}
	return fx.Module(
		"golusoris.leader.pg."+key,
		status,
		fx.Invoke(fx.Annotate(
			func(lc fx.Lifecycle, cfg *config.Config, pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger, st *leader.Status) error {
				opts, loadErr := loadOptionsAt(cfg, hook.ConfigPath(key))
				if loadErr != nil {
					return loadErr
				}
				wire(lc, opts, pool, clk, logger, "leader/pg["+key+"]", st.Observe(cb))
				return nil
			},
			fx.ParamTags("", "", "", "", "", tag),
		)),
	)
}

func wire(lc fx.Lifecycle, opts Options, pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger, name string, cb leader.Callbacks) {
	if !opts.Enabled {
		return
	}
	hook.RunUntilStop(lc, logger, name, func(ctx context.Context) error {
		return Run(ctx, pool, opts, clk, cb)
	})
}
