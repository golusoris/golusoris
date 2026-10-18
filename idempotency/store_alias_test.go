// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/idempotency/postgres"
	"github.com/golusoris/golusoris/idempotency/redis"
	"github.com/golusoris/golusoris/idempotency/sqlite"
	"github.com/golusoris/golusoris/idempotency/store"
)

// Function parameters require identical types, including named interfaces.
var (
	_ func(Store)          = func(store.Store) {}
	_ func(Sweeper)        = func(store.Sweeper) {}
	_ func(ClaimState)     = func(store.ClaimState) {}
	_ func(ClaimResult)    = func(store.ClaimResult) {}
	_ func(CachedResponse) = func(store.CachedResponse) {}
	_ func(*MemoryStore)   = func(*store.MemoryStore) {}
	_ func(*PostgresStore) = func(*postgres.Store) {}
	_ func(*RedisStore)    = func(*redis.Store) {}
	_ func(*SQLiteStore)   = func(*sqlite.Store) {}
)

func TestStoreCompatibilitySentinels(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]error{
		{ErrConflict, store.ErrConflict},
		{ErrFingerprintMismatch, store.ErrFingerprintMismatch},
		{ErrRequestBodyTooLarge, store.ErrRequestBodyTooLarge},
		{ErrReservationLost, store.ErrReservationLost},
	} {
		require.Same(t, pair[0], pair[1])
	}
}
