// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

func readiness(t *testing.T, reg *statuspage.Registry) statuspage.Result {
	t.Helper()
	results := reg.RunTagged(t.Context(), health.TagReadiness)
	require.Len(t, results, 1)
	return results[0]
}

func TestReadinessCheckNilPoolIsDown(t *testing.T) {
	t.Parallel()
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(dbpgx.ReadinessCheck(nil, 0, nil))
	res := readiness(t, reg)
	require.Equal(t, dbpgx.ReadinessCheckName, res.Name)
	require.Equal(t, statuspage.StatusDown, res.Status)
}

func TestReadinessCheckRealPostgres(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(dbpgx.ReadinessCheck(pool, time.Second, nil))
	require.Equal(t, statuspage.StatusUp, readiness(t, reg).Status)
}

func TestReadinessCheckClosedPoolIsDown(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(t.Context(), pgtest.DSN(t))
	require.NoError(t, err)
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(dbpgx.ReadinessCheck(pool, time.Second, nil))
	require.Equal(t, statuspage.StatusUp, readiness(t, reg).Status)

	pool.Close()
	require.Equal(t, statuspage.StatusDown, readiness(t, reg).Status)
}

func TestReadinessModuleRegistersCheck(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	var reg *statuspage.Registry
	app := fx.New(
		fx.NopLogger,
		fx.Supply(pool),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(statuspage.NewRegistry),
		dbpgx.ReadinessModule,
		fx.Populate(&reg),
	)
	require.NoError(t, app.Err())
	res := readiness(t, reg)
	require.Equal(t, dbpgx.ReadinessCheckName, res.Name)
	require.Equal(t, statuspage.StatusUp, res.Status)
}
