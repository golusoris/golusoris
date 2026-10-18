// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlite_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/idempotency/internal/storetest"
	"github.com/golusoris/golusoris/idempotency/sqlite"
	"github.com/golusoris/golusoris/idempotency/store"
)

func newSQLiteHarness(t *testing.T) storetest.Harness {
	t.Helper()
	clk := clockwork.NewFakeClockAt(storetest.Base())
	store, err := sqlite.New(storetest.OpenSQLite(t), clk)
	require.NoError(t, err)
	require.NoError(t, store.EnsureSchema(t.Context()))
	return storetest.Harness{Store: store, Advance: func(_ *testing.T, d time.Duration) { clk.Advance(d) }}
}

func TestStore_Conformance(t *testing.T) {
	t.Parallel()
	storetest.Run(t, newSQLiteHarness)
}

func TestStore_Sweep(t *testing.T) {
	t.Parallel()
	h := newSQLiteHarness(t)
	sweeper, ok := h.Store.(store.Sweeper)
	require.True(t, ok, "Store implements Sweeper")
	storetest.RunSweep(t, h, sweeper)
}

func TestStore_SharedAcrossStores(t *testing.T) {
	t.Parallel()
	db := storetest.OpenSQLite(t)
	replicaA, err := sqlite.New(db, nil)
	require.NoError(t, err)
	require.NoError(t, replicaA.EnsureSchema(t.Context()))
	replicaB, err := sqlite.New(db, nil)
	require.NoError(t, err)
	require.NoError(t, replicaB.EnsureSchema(t.Context()), "EnsureSchema is idempotent")
	storetest.SharedAcrossReplicas(t, replicaA, replicaB)
}

func TestStore_MissingSchemaFails(t *testing.T) {
	t.Parallel()
	store, err := sqlite.New(storetest.OpenSQLite(t), nil)
	require.NoError(t, err)
	_, err = store.Claim(t.Context(), "key", "fingerprint-a", time.Minute)
	require.Error(t, err)
}

func TestNew_NilDB(t *testing.T) {
	t.Parallel()
	store, err := sqlite.New(nil, nil)
	require.Error(t, err)
	require.Nil(t, store)
}
