// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package bun wires a [*bun.DB] ORM over the pool provided by db/pgx, as an
// opt-in alternative to hand-written sqlc queries. It borrows the shared
// [*pgxpool.Pool] — db/pgx owns the pool lifecycle — so an app can mix bun and
// sqlc against one connection pool.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.DB,    // provides *pgxpool.Pool
//	    golusoris.DBBun, // provides *bun.DB over that pool
//	    fx.Invoke(func(db *bun.DB) error {
//	        return db.NewCreateTable().Model((*User)(nil)).IfNotExists().Exec(ctx)
//	    }),
//	)
package bun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/extra/bundebug"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

// Options tunes the bun ORM. Config keys live under the "db.bun" prefix.
type Options struct {
	// Verbose installs bun's debug query hook (logs every statement). Leave off
	// in production — query timing already flows through db/pgx's slow-query
	// tracer.
	Verbose bool `koanf:"verbose"`
}

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("db.bun", &opts); err != nil {
		return Options{}, fmt.Errorf("db/bun: load options: %w", err)
	}
	return opts, nil
}

// New builds a [*bun.DB] over the shared pgx pool. The returned DB borrows the
// pool. Direct callers close the Bun adapter when done; that does not close the
// shared pool.
func New(pool *pgxpool.Pool, opts Options, logger *slog.Logger) (*bun.DB, error) {
	if err := validatePool(pool); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	db := bun.NewDB(stdlib.OpenDBFromPool(pool), pgdialect.New())
	if opts.Verbose {
		db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	}
	logger.Debug("db/bun: ORM ready", slog.Bool("verbose", opts.Verbose))
	return db, nil
}

func validatePool(pool *pgxpool.Pool) (err error) {
	if pool == nil {
		return errors.New("db/bun: nil pool")
	}
	defer func() {
		if recover() != nil {
			err = errors.New("db/bun: invalid pool")
		}
	}()
	if pool.Config() == nil {
		return errors.New("db/bun: invalid pool")
	}
	return nil
}

// Module provides a [*bun.DB] built over the db/pgx [*pgxpool.Pool]. Requires
// [golusoris.DB] (the pool) + [golusoris.Core] (config + log) in the graph.
var Module = fx.Module(
	"golusoris.db.bun",
	fx.Provide(loadOptions),
	fx.Provide(New),
	fx.Invoke(registerLifecycle),
)

func registerLifecycle(lc fx.Lifecycle, db *bun.DB) error {
	if db == nil {
		return errors.New("db/bun: nil database")
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		if err := db.Close(); err != nil {
			return fmt.Errorf("db/bun: close adapter: %w", err)
		}
		return nil
	}})
	return nil
}
