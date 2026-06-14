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
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/extra/bundebug"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
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
// pool — db/pgx owns the connection lifecycle, so it is intentionally not
// closed here (closing it would tear down the shared pool).
func New(pool *pgxpool.Pool, opts Options, logger *slog.Logger) *bun.DB {
	db := bun.NewDB(stdlib.OpenDBFromPool(pool), pgdialect.New())
	if opts.Verbose {
		db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	}
	logger.Debug("db/bun: ORM ready", slog.Bool("verbose", opts.Verbose))
	return db
}

// Module provides a [*bun.DB] built over the db/pgx [*pgxpool.Pool]. Requires
// [golusoris.DB] (the pool) + [golusoris.Core] (config + log) in the graph.
var Module = fx.Module("golusoris.db.bun",
	fx.Provide(loadOptions),
	fx.Provide(New),
)
