// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package postgres provides the Postgres idempotency backend.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency/internal/sqlstore"
	"github.com/golusoris/golusoris/idempotency/store"
)

// MigrationsFS holds the PostgreSQL schema for [Store]; apply it
// via db/migrate (Options.WithFS) or bundle it into the app's migration set.
//
//go:embed migrations
var MigrationsFS embed.FS

const (
	pgReserveSQL = `INSERT INTO golusoris_idempotency_keys
    (scope_key, state, token, fingerprint, expires_at)
VALUES ($1, 1, $2, $3, $4)
ON CONFLICT (scope_key) DO UPDATE
SET state = 1, token = EXCLUDED.token, fingerprint = EXCLUDED.fingerprint,
    status_code = 0, header = '{}'::jsonb, body = ''::bytea,
    expires_at = EXCLUDED.expires_at
WHERE golusoris_idempotency_keys.expires_at <= $5`
	pgLookupSQL = `SELECT state, fingerprint, status_code, header, body
FROM golusoris_idempotency_keys
WHERE scope_key = $1 AND expires_at > $2`
	pgCompleteSQL = `UPDATE golusoris_idempotency_keys
SET state = 2, status_code = $4, header = $5, body = $6, expires_at = $7
WHERE scope_key = $1 AND token = $2 AND fingerprint = $3
  AND state = 1 AND expires_at > $8`
	pgOwnerSQL = `SELECT fingerprint FROM golusoris_idempotency_keys
WHERE scope_key = $1 AND token = $2 AND state = 1 AND expires_at > $3`
	pgReleaseSQL = `DELETE FROM golusoris_idempotency_keys
WHERE scope_key = $1 AND token = $2 AND state = 1`
	// SKIP LOCKED leaves rows a concurrent Claim is taking over to that Claim.
	pgSweepSQL = `DELETE FROM golusoris_idempotency_keys
WHERE scope_key IN (
    SELECT scope_key FROM golusoris_idempotency_keys
    WHERE expires_at <= $1
    ORDER BY expires_at
    LIMIT $2
    FOR UPDATE SKIP LOCKED
) AND expires_at <= $1`
)

type sqlStore = sqlstore.Store

// Store is a [store.Store] shared by every replica through one PostgreSQL
// table. Apply [MigrationsFS] before use. Expiry uses the injected clock, so
// replica clock skew shifts expiry by the skew.
type Store struct {
	sqlStore
}

var (
	_ store.Store   = (*Store)(nil)
	_ store.Sweeper = (*Store)(nil)
)

// New returns a Store over pool. A nil clock uses the
// real clock.
func New(pool *pgxpool.Pool, clk clockwork.Clock) (*Store, error) {
	if pool == nil {
		return nil, errors.New("idempotency: postgres store: nil pool")
	}
	return &Store{sqlStore: sqlstore.New(pgRows{pool: pool}, clk)}, nil
}

// pgRows is the PostgreSQL statement set.
type pgRows struct {
	pool *pgxpool.Pool
}

func (p pgRows) Reserve(ctx context.Context, key, token, fingerprint string, now, expires time.Time) (bool, error) {
	tag, err := p.pool.Exec(ctx, pgReserveSQL, key, token, fingerprint, expires, now)
	if err != nil {
		return false, fmt.Errorf("postgres reserve: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (p pgRows) Lookup(ctx context.Context, key string, now time.Time) (sqlstore.Record, bool, error) {
	var record sqlstore.Record
	var state int16
	err := p.pool.QueryRow(ctx, pgLookupSQL, key, now).
		Scan(&state, &record.Fingerprint, &record.Status, &record.Header, &record.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlstore.Record{}, false, nil
	}
	if err != nil {
		return sqlstore.Record{}, false, fmt.Errorf("postgres lookup: %w", err)
	}
	record.State = int(state)
	return record, true, nil
}

func (p pgRows) Complete(ctx context.Context, done sqlstore.Completion) (bool, error) {
	tag, err := p.pool.Exec(ctx, pgCompleteSQL,
		done.Key, done.Token, done.Fingerprint, done.Status, done.Header, done.Body, done.Expires, done.Now)
	if err != nil {
		return false, fmt.Errorf("postgres complete: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (p pgRows) Owner(ctx context.Context, key, token string, now time.Time) (string, bool, error) {
	var fingerprint string
	err := p.pool.QueryRow(ctx, pgOwnerSQL, key, token, now).Scan(&fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("postgres owner: %w", err)
	}
	return fingerprint, true, nil
}

func (p pgRows) Release(ctx context.Context, key, token string) (bool, error) {
	tag, err := p.pool.Exec(ctx, pgReleaseSQL, key, token)
	if err != nil {
		return false, fmt.Errorf("postgres release: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (p pgRows) Sweep(ctx context.Context, now time.Time, limit int) (int64, error) {
	tag, err := p.pool.Exec(ctx, pgSweepSQL, now, limit)
	if err != nil {
		return 0, fmt.Errorf("postgres sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}
