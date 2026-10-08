// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	dbsqlite "github.com/golusoris/golusoris/db/sqlite"
	"github.com/golusoris/golusoris/idempotency"
)

// openSQLite opens a file database so concurrent claims use real locking.
func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := dbsqlite.Open(t.Context(), dbsqlite.Options{
		Path: filepath.Join(t.TempDir(), "idempotency.db"),
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func newSQLiteHarness(t *testing.T) storeHarness {
	t.Helper()
	clk := clockwork.NewFakeClockAt(conformanceBase())
	store, err := idempotency.NewSQLiteStore(openSQLite(t), clk)
	require.NoError(t, err)
	require.NoError(t, store.EnsureSchema(t.Context()))
	return storeHarness{store: store, advance: func(_ *testing.T, d time.Duration) { clk.Advance(d) }}
}

func TestSQLiteStore_Conformance(t *testing.T) {
	t.Parallel()
	runStoreConformance(t, newSQLiteHarness)
}

func TestSQLiteStore_Sweep(t *testing.T) {
	t.Parallel()
	h := newSQLiteHarness(t)
	sweeper, ok := h.store.(idempotency.Sweeper)
	require.True(t, ok, "SQLiteStore implements Sweeper")
	runSweepConformance(t, h, sweeper)
}

func TestSQLiteStore_SharedAcrossStores(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)
	replicaA, err := idempotency.NewSQLiteStore(db, nil)
	require.NoError(t, err)
	require.NoError(t, replicaA.EnsureSchema(t.Context()))
	replicaB, err := idempotency.NewSQLiteStore(db, nil)
	require.NoError(t, err)
	require.NoError(t, replicaB.EnsureSchema(t.Context()), "EnsureSchema is idempotent")
	requireSharedAcrossReplicas(t, replicaA, replicaB)
}

func TestSQLiteStore_MissingSchemaFails(t *testing.T) {
	t.Parallel()
	store, err := idempotency.NewSQLiteStore(openSQLite(t), nil)
	require.NoError(t, err)
	_, err = store.Claim(t.Context(), "key", fingerprintA, time.Minute)
	require.Error(t, err)
}

func TestNewSQLiteStore_NilDB(t *testing.T) {
	t.Parallel()
	store, err := idempotency.NewSQLiteStore(nil, nil)
	require.Error(t, err)
	require.Nil(t, store)
}
