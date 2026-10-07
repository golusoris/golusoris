// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package timescale provides TimescaleDB hypertable helpers for pgx/v5.
//
// TimescaleDB extends PostgreSQL — the same pgx pool used for regular tables
// works here. This package adds helpers for hypertables, chunk intervals,
// compression, retention, and continuous aggregates, and detects the
// installed edition first: TSL-only features return [ErrUnsupportedEdition]
// under the Apache build and [ErrExtensionMissing] without the extension.
//
// Usage:
//
//	pool, _ := pgxpool.New(ctx, dsn) // TimescaleDB-enabled Postgres
//	ts := timescale.New(pool)
//
//	_ = ts.CreateHypertable(ctx, "metrics", "time")
//	_ = ts.SetRetention(ctx, "metrics", 30*24*time.Hour)
package timescale

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is the pgx surface the helpers use; tests substitute a fake.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var _ querier = (*pgxpool.Pool)(nil)

// DB wraps a pgxpool.Pool with TimescaleDB-specific helpers.
type DB struct {
	pool        *pgxpool.Pool
	db          querier
	initialized bool
}

// New returns a TimescaleDB helper backed by pool.
// The pool must connect to a TimescaleDB-enabled PostgreSQL instance.
func New(pool *pgxpool.Pool) *DB {
	d := &DB{pool: pool, initialized: poolInitialized(pool)}
	if pool != nil {
		d.db = pool
	}
	return d
}

// HypertableOptions tunes [DB.CreateHypertableWithOptions].
type HypertableOptions struct {
	// ChunkTimeInterval is the time range one chunk covers. Zero keeps the
	// TimescaleDB default (7 days); an existing hypertable gets the new
	// interval for chunks created afterwards.
	ChunkTimeInterval time.Duration
}

// CreateHypertable converts an existing table into a TimescaleDB hypertable
// partitioned on timeColumn. Idempotent: succeeds if the hypertable already exists.
// Without the extension it returns [ErrExtensionMissing] and the table stays plain.
func (d *DB) CreateHypertable(ctx context.Context, table, timeColumn string) error {
	return d.CreateHypertableWithOptions(ctx, table, timeColumn, HypertableOptions{})
}

// CreateHypertableWithOptions is [DB.CreateHypertable] with a chunk interval
// for timestamp, timestamptz, or date time columns.
func (d *DB) CreateHypertableWithOptions(ctx context.Context, table, timeColumn string, opts HypertableOptions) error {
	if table == "" || timeColumn == "" {
		return errors.New("timescale: create_hypertable: table and time column are required")
	}
	interval, err := optionalInterval(opts.ChunkTimeInterval)
	if err != nil {
		return fmt.Errorf("timescale: chunk time interval: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	if reqErr := d.require(ctx, FeatureHypertable); reqErr != nil {
		return reqErr
	}
	created, err := d.createHypertable(ctx, table, timeColumn, interval)
	if err != nil || created || !interval.Valid {
		return err
	}
	if _, err = d.db.Exec(ctx, "SELECT set_chunk_time_interval($1, ($2)::interval)", table, interval); err != nil {
		return fmt.Errorf("timescale: set_chunk_time_interval %s: %w", table, err)
	}
	return nil
}

// createHypertable reports whether the call converted the table; an
// existing hypertable is skipped by if_not_exists so startup stays safe.
func (d *DB) createHypertable(ctx context.Context, table, timeColumn string, interval pgtype.Interval) (bool, error) {
	query, args := "SELECT created FROM create_hypertable($1, by_range($2), if_not_exists => true)", []any{table, timeColumn}
	if interval.Valid {
		query = "SELECT created FROM create_hypertable($1, by_range($2, ($3)::interval), if_not_exists => true)"
		args = append(args, interval)
	}
	var created bool
	if err := d.db.QueryRow(ctx, query, args...).Scan(&created); err != nil {
		return false, fmt.Errorf("timescale: create_hypertable %s: %w", table, err)
	}
	return created, nil
}

// SetRetention configures a data-retention policy that drops chunks older than
// duration. Call after CreateHypertable. The policy is a TSL background job:
// under the Apache edition it returns [ErrUnsupportedEdition]; schedule
// [DB.DropChunks] from the application instead.
func (d *DB) SetRetention(ctx context.Context, table string, duration time.Duration) error {
	interval, err := intervalValue(duration)
	if err != nil {
		return fmt.Errorf("timescale: retention duration: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	if reqErr := d.require(ctx, FeatureRetentionPolicy); reqErr != nil {
		return reqErr
	}
	_, err = d.db.Exec(
		ctx,
		"SELECT add_retention_policy($1, ($2)::interval, if_not_exists => true)",
		table, interval,
	)
	if err != nil {
		return fmt.Errorf("timescale: add_retention_policy %s: %w", table, err)
	}
	return nil
}

// DropChunks drops chunks of table whose data is older than olderThan and
// returns how many it dropped. It works under every edition, so it is the
// retention fallback when [DB.SetRetention] reports [ErrUnsupportedEdition].
func (d *DB) DropChunks(ctx context.Context, table string, olderThan time.Duration) (int, error) {
	interval, err := intervalValue(olderThan)
	if err != nil {
		return 0, fmt.Errorf("timescale: drop_chunks duration: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return 0, poolErr
	}
	if reqErr := d.require(ctx, FeatureDropChunks); reqErr != nil {
		return 0, reqErr
	}
	var dropped int
	err = d.db.QueryRow(
		ctx,
		"SELECT count(*) FROM drop_chunks($1, older_than => ($2)::interval)",
		table, interval,
	).Scan(&dropped)
	if err != nil {
		return 0, fmt.Errorf("timescale: drop_chunks %s: %w", table, err)
	}
	return dropped, nil
}

// EnableCompression enables TimescaleDB columnar compression on the hypertable.
func (d *DB) EnableCompression(ctx context.Context, table string) error {
	return d.EnableCompressionWithOptions(ctx, table, CompressionOptions{})
}

// EnableCompressionWithOptions enables compression with segment-by and
// order-by columns. Requires the community edition.
func (d *DB) EnableCompressionWithOptions(ctx context.Context, table string, opts CompressionOptions) error {
	query, err := compressionSQL(table, opts)
	if err != nil {
		return fmt.Errorf("timescale: enable compression: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	if reqErr := d.require(ctx, FeatureCompression); reqErr != nil {
		return reqErr
	}
	if _, err = d.db.Exec(ctx, query); err != nil {
		return fmt.Errorf("timescale: enable compression %s: %w", table, err)
	}
	return nil
}

// AddCompressionPolicy adds an automatic compression policy that compresses
// chunks older than olderThan. Requires the community edition.
func (d *DB) AddCompressionPolicy(ctx context.Context, table string, olderThan time.Duration) error {
	interval, err := intervalValue(olderThan)
	if err != nil {
		return fmt.Errorf("timescale: compression duration: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	if reqErr := d.require(ctx, FeatureCompression); reqErr != nil {
		return reqErr
	}
	_, err = d.db.Exec(
		ctx,
		"SELECT add_compression_policy($1, ($2)::interval, if_not_exists => true)",
		table, interval,
	)
	if err != nil {
		return fmt.Errorf("timescale: add_compression_policy %s: %w", table, err)
	}
	return nil
}

// Pool returns the underlying pgxpool.Pool.
func (d *DB) Pool() *pgxpool.Pool {
	if d == nil || !d.initialized {
		return nil
	}
	return d.pool
}

func (d *DB) validatePool() error {
	if d == nil || d.db == nil {
		return errors.New("timescale: nil pool")
	}
	if !d.initialized {
		return errors.New("timescale: invalid pool")
	}
	return nil
}

func poolInitialized(pool *pgxpool.Pool) (initialized bool) {
	if pool == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			initialized = false
		}
	}()
	return pool.Config() != nil
}

func intervalValue(duration time.Duration) (pgtype.Interval, error) {
	if duration <= 0 {
		return pgtype.Interval{}, errors.New("duration must be positive")
	}
	if duration%time.Microsecond != 0 {
		return pgtype.Interval{}, errors.New("duration must have microsecond precision")
	}
	return pgtype.Interval{Microseconds: duration.Microseconds(), Valid: true}, nil
}

// relationIdent quotes table or schema.table as a SQL identifier.
func relationIdent(table string) (string, error) {
	if table == "" {
		return "", errors.New("table is required")
	}
	if strings.ContainsRune(table, '\x00') {
		return "", errors.New("table contains NUL")
	}
	parts := strings.Split(table, ".")
	if len(parts) > 2 || slices.Contains(parts, "") {
		return "", errors.New("table must be an identifier or schema-qualified identifier")
	}
	return pgx.Identifier(parts).Sanitize(), nil
}
