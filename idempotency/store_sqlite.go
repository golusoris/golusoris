// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jonboulle/clockwork"
)

const (
	sqliteTableSQL = `CREATE TABLE IF NOT EXISTS golusoris_idempotency_keys (
    scope_key   TEXT    PRIMARY KEY,
    state       INTEGER NOT NULL,
    token       TEXT    NOT NULL,
    fingerprint TEXT    NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    header      TEXT    NOT NULL DEFAULT '{}',
    body        BLOB    NOT NULL DEFAULT x'',
    expires_at  INTEGER NOT NULL
)`
	sqliteIndexSQL = `CREATE INDEX IF NOT EXISTS golusoris_idempotency_keys_expires_idx
    ON golusoris_idempotency_keys (expires_at)`
	sqliteReserveSQL = `INSERT INTO golusoris_idempotency_keys
    (scope_key, state, token, fingerprint, expires_at)
VALUES (?1, 1, ?2, ?3, ?4)
ON CONFLICT (scope_key) DO UPDATE
SET state = 1, token = excluded.token, fingerprint = excluded.fingerprint,
    status_code = 0, header = '{}', body = x'', expires_at = excluded.expires_at
WHERE golusoris_idempotency_keys.expires_at <= ?5`
	sqliteLookupSQL = `SELECT state, fingerprint, status_code, header, body
FROM golusoris_idempotency_keys
WHERE scope_key = ?1 AND expires_at > ?2`
	sqliteCompleteSQL = `UPDATE golusoris_idempotency_keys
SET state = 2, status_code = ?4, header = ?5, body = ?6, expires_at = ?7
WHERE scope_key = ?1 AND token = ?2 AND fingerprint = ?3
  AND state = 1 AND expires_at > ?8`
	sqliteOwnerSQL = `SELECT fingerprint FROM golusoris_idempotency_keys
WHERE scope_key = ?1 AND token = ?2 AND state = 1 AND expires_at > ?3`
	sqliteReleaseSQL = `DELETE FROM golusoris_idempotency_keys
WHERE scope_key = ?1 AND token = ?2 AND state = 1`
	sqliteSweepSQL = `DELETE FROM golusoris_idempotency_keys
WHERE scope_key IN (
    SELECT scope_key FROM golusoris_idempotency_keys
    WHERE expires_at <= ?1
    ORDER BY expires_at
    LIMIT ?2
)`
)

// SQLiteStore is a [Store] for standalone single-node deployments over a
// database/sql handle (db/sqlite). Call [SQLiteStore.EnsureSchema] once
// before use. Expiry instants persist as Unix nanoseconds.
type SQLiteStore struct {
	sqlStore

	db *sql.DB
}

var (
	_ Store   = (*SQLiteStore)(nil)
	_ Sweeper = (*SQLiteStore)(nil)
)

// NewSQLiteStore returns a SQLiteStore over db. A nil clock uses the real
// clock.
func NewSQLiteStore(db *sql.DB, clk clockwork.Clock) (*SQLiteStore, error) {
	if db == nil {
		return nil, errors.New("idempotency: sqlite store: nil database")
	}
	return &SQLiteStore{sqlStore: newSQLStore(sqliteRows{db: db}, clk), db: db}, nil
}

// EnsureSchema creates the store table and its expiry index when absent.
func (s *SQLiteStore) EnsureSchema(ctx context.Context) error {
	for _, statement := range []string{sqliteTableSQL, sqliteIndexSQL} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("idempotency: sqlite schema: %w", err)
		}
	}
	return nil
}

// sqliteRows is the SQLite statement set.
type sqliteRows struct {
	db *sql.DB
}

func (s sqliteRows) reserve(ctx context.Context, key, token, fingerprint string, now, expires time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, sqliteReserveSQL, key, token, fingerprint, expires.UnixNano(), now.UnixNano())
	if err != nil {
		return false, fmt.Errorf("sqlite reserve: %w", err)
	}
	return affectedOne(result)
}

func (s sqliteRows) lookup(ctx context.Context, key string, now time.Time) (sqlRecord, bool, error) {
	var record sqlRecord
	err := s.db.QueryRowContext(ctx, sqliteLookupSQL, key, now.UnixNano()).
		Scan(&record.state, &record.fingerprint, &record.status, &record.header, &record.body)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlRecord{}, false, nil
	}
	if err != nil {
		return sqlRecord{}, false, fmt.Errorf("sqlite lookup: %w", err)
	}
	return record, true, nil
}

func (s sqliteRows) complete(ctx context.Context, done completion) (bool, error) {
	result, err := s.db.ExecContext(ctx, sqliteCompleteSQL,
		done.key, done.token, done.fingerprint, done.status, string(done.header), done.body,
		done.expires.UnixNano(), done.now.UnixNano())
	if err != nil {
		return false, fmt.Errorf("sqlite complete: %w", err)
	}
	return affectedOne(result)
}

func (s sqliteRows) owner(ctx context.Context, key, token string, now time.Time) (string, bool, error) {
	var fingerprint string
	err := s.db.QueryRowContext(ctx, sqliteOwnerSQL, key, token, now.UnixNano()).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite owner: %w", err)
	}
	return fingerprint, true, nil
}

func (s sqliteRows) release(ctx context.Context, key, token string) (bool, error) {
	result, err := s.db.ExecContext(ctx, sqliteReleaseSQL, key, token)
	if err != nil {
		return false, fmt.Errorf("sqlite release: %w", err)
	}
	return affectedOne(result)
}

func (s sqliteRows) sweep(ctx context.Context, now time.Time, limit int) (int64, error) {
	result, err := s.db.ExecContext(ctx, sqliteSweepSQL, now.UnixNano(), limit)
	if err != nil {
		return 0, fmt.Errorf("sqlite sweep: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite sweep: rows affected: %w", err)
	}
	return removed, nil
}

func affectedOne(result sql.Result) (bool, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return affected == 1, nil
}
