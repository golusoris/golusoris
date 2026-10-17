// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package postgres_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/idempotency/internal/storetest"
	"github.com/golusoris/golusoris/idempotency/postgres"
	"github.com/golusoris/golusoris/idempotency/store"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// startIdempotencyPG boots Postgres and applies postgres.MigrationsFS.
func startIdempotencyPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Start(t)
	migrator, err := dbmigrate.New(
		dbmigrate.Options{Path: "migrations"}.WithFS(postgres.MigrationsFS),
		dbpgx.Options{DSN: pool.Config().ConnString()},
		slog.New(slog.DiscardHandler),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, migrator.Close()) }()
	require.NoError(t, migrator.Up())
	return pool
}

func newPostgresHarness(pool *pgxpool.Pool, start time.Time) func(t *testing.T) storetest.Harness {
	return func(t *testing.T) storetest.Harness {
		t.Helper()
		clk := clockwork.NewFakeClockAt(start)
		store, err := postgres.New(pool, clk)
		require.NoError(t, err)
		return storetest.Harness{Store: store, Advance: func(_ *testing.T, d time.Duration) { clk.Advance(d) }}
	}
}

func TestStore_Conformance(t *testing.T) {
	t.Parallel()
	pool := startIdempotencyPG(t)
	t.Run("contract", func(t *testing.T) {
		t.Parallel()
		storetest.Run(t, newPostgresHarness(pool, storetest.Base()))
	})
	// Rows from the contract cases share the table; a clock far behind them
	// keeps the sweep from counting their expired rows.
	sweepHarness := newPostgresHarness(pool, storetest.Base().AddDate(-10, 0, 0))(t)
	sweeper, ok := sweepHarness.Store.(store.Sweeper)
	require.True(t, ok, "Store implements Sweeper")
	storetest.RunSweep(t, sweepHarness, sweeper)
}

// TestStore_SharedAcrossReplicas is the #624 acceptance: two
// replicas, each with its own pool and store, execute a key once and replay
// it on the other replica.
func TestStore_SharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	pool := startIdempotencyPG(t)
	second, err := pgxpool.New(t.Context(), pool.Config().ConnString())
	require.NoError(t, err)
	t.Cleanup(second.Close)
	replicaA, err := postgres.New(pool, nil)
	require.NoError(t, err)
	replicaB, err := postgres.New(second, nil)
	require.NoError(t, err)
	storetest.SharedAcrossReplicas(t, replicaA, replicaB)
}

func TestNew_NilPool(t *testing.T) {
	t.Parallel()
	store, err := postgres.New(nil, nil)
	require.Error(t, err)
	require.Nil(t, store)
}
