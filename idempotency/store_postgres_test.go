// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/idempotency"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// startIdempotencyPG boots Postgres and applies idempotency.MigrationsFS.
func startIdempotencyPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.Start(t)
	migrator, err := dbmigrate.New(
		dbmigrate.Options{Path: "migrations"}.WithFS(idempotency.MigrationsFS),
		dbpgx.Options{DSN: pool.Config().ConnString()},
		slog.New(slog.DiscardHandler),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, migrator.Close()) }()
	require.NoError(t, migrator.Up())
	return pool
}

func newPostgresHarness(pool *pgxpool.Pool, start time.Time) func(t *testing.T) storeHarness {
	return func(t *testing.T) storeHarness {
		t.Helper()
		clk := clockwork.NewFakeClockAt(start)
		store, err := idempotency.NewPostgresStore(pool, clk)
		require.NoError(t, err)
		return storeHarness{store: store, advance: func(_ *testing.T, d time.Duration) { clk.Advance(d) }}
	}
}

func TestPostgresStore_Conformance(t *testing.T) {
	t.Parallel()
	pool := startIdempotencyPG(t)
	t.Run("contract", func(t *testing.T) {
		t.Parallel()
		runStoreConformance(t, newPostgresHarness(pool, conformanceBase()))
	})
	// Rows from the contract cases share the table; a clock far behind them
	// keeps the sweep from counting their expired rows.
	sweepHarness := newPostgresHarness(pool, conformanceBase().AddDate(-10, 0, 0))(t)
	sweeper, ok := sweepHarness.store.(idempotency.Sweeper)
	require.True(t, ok, "PostgresStore implements Sweeper")
	runSweepConformance(t, sweepHarness, sweeper)
}

// TestPostgresStore_SharedAcrossReplicas is the #624 acceptance: two
// replicas, each with its own pool and store, execute a key once and replay
// it on the other replica.
func TestPostgresStore_SharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	pool := startIdempotencyPG(t)
	second, err := pgxpool.New(t.Context(), pool.Config().ConnString())
	require.NoError(t, err)
	t.Cleanup(second.Close)
	replicaA, err := idempotency.NewPostgresStore(pool, nil)
	require.NoError(t, err)
	replicaB, err := idempotency.NewPostgresStore(second, nil)
	require.NoError(t, err)
	requireSharedAcrossReplicas(t, replicaA, replicaB)
}

func TestNewPostgresStore_NilPool(t *testing.T) {
	t.Parallel()
	store, err := idempotency.NewPostgresStore(nil, nil)
	require.Error(t, err)
	require.Nil(t, store)
}
