// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

// ReadinessCheckName names the check that [ReadinessModule] registers.
const ReadinessCheckName = "postgres"

// ErrNilPool reports a readiness check built without a pool.
var ErrNilPool = errors.New("db/pgx: nil pool")

// ReadinessCheck returns a readiness-tagged check that pings pool within
// timeout (<= 0 uses [health.DefaultDependencyTimeout]). A non-nil logger
// records failure causes; the public message stays generic.
func ReadinessCheck(pool *pgxpool.Pool, timeout time.Duration, logger *slog.Logger) statuspage.Check {
	return health.DependencyCheck(ReadinessCheckName, timeout, logger, func(ctx context.Context) error {
		if pool == nil {
			return ErrNilPool
		}
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("db/pgx: ping: %w", err)
		}
		return nil
	})
}

// ReadinessModule registers [ReadinessCheck] for the graph's pool on the
// app's *statuspage.Registry, so /readyz fails while Postgres is unreachable.
// Opt-in; requires a *pgxpool.Pool ([Module]), *slog.Logger, and a
// *statuspage.Registry.
var ReadinessModule = fx.Module(
	"golusoris.db.pgx.readiness",
	fx.Invoke(registerReadiness),
)

func registerReadiness(reg *statuspage.Registry, pool *pgxpool.Pool, logger *slog.Logger) {
	reg.Register(ReadinessCheck(pool, health.DefaultDependencyTimeout, logger))
}
