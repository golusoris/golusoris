// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/redis/rueidis"

	"github.com/golusoris/golusoris/idempotency/postgres"
	"github.com/golusoris/golusoris/idempotency/redis"
	"github.com/golusoris/golusoris/idempotency/sqlite"
	"github.com/golusoris/golusoris/idempotency/store"
)

// CachedResponse preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.CachedResponse.
type CachedResponse = store.CachedResponse

// ClaimState preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ClaimState.
type ClaimState = store.ClaimState

// ClaimResult preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ClaimResult.
type ClaimResult = store.ClaimResult

// Store preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.Store.
type Store = store.Store

// Sweeper preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.Sweeper.
type Sweeper = store.Sweeper

// MemoryStore preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.MemoryStore.
type MemoryStore = store.MemoryStore

// PostgresStore preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/postgres.Store.
type PostgresStore = postgres.Store

// RedisStore preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/redis.Store.
type RedisStore = redis.Store

// SQLiteStore preserves the original store API.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/sqlite.Store.
type SQLiteStore = sqlite.Store

// ClaimAcquired preserves the original store constant.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ClaimAcquired.
const ClaimAcquired = store.ClaimAcquired

// ClaimInFlight preserves the original store constant.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ClaimInFlight.
const ClaimInFlight = store.ClaimInFlight

// ClaimCompleted preserves the original store constant.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ClaimCompleted.
const ClaimCompleted = store.ClaimCompleted

// ClaimFingerprintMismatch preserves the original store constant.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ClaimFingerprintMismatch.
const ClaimFingerprintMismatch = store.ClaimFingerprintMismatch

// MaxSweepBatch preserves the original store constant.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.MaxSweepBatch.
const MaxSweepBatch = store.MaxSweepBatch

// DefaultRedisPrefix preserves the original store constant.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/redis.DefaultRedisPrefix.
const DefaultRedisPrefix = redis.DefaultRedisPrefix

// ErrConflict preserves the original sentinel.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ErrConflict.
var ErrConflict = store.ErrConflict

// ErrFingerprintMismatch preserves the original sentinel.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ErrFingerprintMismatch.
var ErrFingerprintMismatch = store.ErrFingerprintMismatch

// ErrRequestBodyTooLarge preserves the original sentinel.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ErrRequestBodyTooLarge.
var ErrRequestBodyTooLarge = store.ErrRequestBodyTooLarge

// ErrReservationLost preserves the original sentinel.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.ErrReservationLost.
var ErrReservationLost = store.ErrReservationLost

// MigrationsFS holds the PostgreSQL schema.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/postgres.MigrationsFS.
var MigrationsFS = postgres.MigrationsFS

// NewMemoryStore preserves the original constructor.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.NewMemoryStore.
func NewMemoryStore() *MemoryStore { return store.NewMemoryStore() }

// NewMemoryStoreWithClock preserves the original constructor.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/store.NewMemoryStoreWithClock.
func NewMemoryStoreWithClock(clk clockwork.Clock) *MemoryStore {
	return store.NewMemoryStoreWithClock(clk)
}

// NewPostgresStore preserves the original constructor.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/postgres.New.
func NewPostgresStore(pool *pgxpool.Pool, clk clockwork.Clock) (*PostgresStore, error) {
	value, err := postgres.New(pool, clk)
	if err != nil {
		return nil, fmt.Errorf("idempotency: postgres store: %w", err)
	}
	return value, nil
}

// NewRedisStore preserves the original constructor.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/redis.New.
func NewRedisStore(client rueidis.Client, prefix string) (*RedisStore, error) {
	value, err := redis.New(client, prefix)
	if err != nil {
		return nil, fmt.Errorf("idempotency: redis store: %w", err)
	}
	return value, nil
}

// NewSQLiteStore preserves the original constructor.
//
// Deprecated: use github.com/golusoris/golusoris/idempotency/sqlite.New.
func NewSQLiteStore(db *sql.DB, clk clockwork.Clock) (*SQLiteStore, error) {
	value, err := sqlite.New(db, clk)
	if err != nil {
		return nil, fmt.Errorf("idempotency: sqlite store: %w", err)
	}
	return value, nil
}
