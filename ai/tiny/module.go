package tiny

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
)

// Module provides a durable [Registry] backed by PostgreSQL.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,    // config + log + clock
//	    golusoris.DB,      // *pgxpool.Pool
//	    tiny.Module,       // provides tiny.Registry (*PGRegistry)
//	)
//
// The schema lives in ai/tiny/migrations; apply it via db/migrate with
// [MigrationsFS] before the app serves traffic. Module provides the
// Registry interface so consumers depend on the contract, not the
// concrete type — swap in [MemoryRegistry] for tests without rewiring.
var Module = fx.Module(
	"golusoris.ai.tiny",
	fx.Provide(
		func(pool *pgxpool.Pool, clk clock.Clock) (Registry, error) {
			return NewPGRegistryWithClock(pool, clk)
		},
	),
)
