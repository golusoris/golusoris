// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package timescale provides TimescaleDB hypertable helpers for pgx/v5.
//
// TimescaleDB extends PostgreSQL — the same pgx pool used for regular tables
// works here. This package adds helpers for creating hypertables, setting
// retention policies, and querying time-bucket aggregations.
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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool.Pool with TimescaleDB-specific helpers.
type DB struct {
	pool        *pgxpool.Pool
	initialized bool
}

// New returns a TimescaleDB helper backed by pool.
// The pool must connect to a TimescaleDB-enabled PostgreSQL instance.
func New(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool, initialized: poolInitialized(pool)}
}

// CreateHypertable converts an existing table into a TimescaleDB hypertable
// partitioned on timeColumn. Idempotent: succeeds if the hypertable already exists.
func (d *DB) CreateHypertable(ctx context.Context, table, timeColumn string) error {
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	// if_not_exists=true makes this safe to call on every startup.
	_, err := d.pool.Exec(
		ctx,
		"SELECT create_hypertable($1, by_range($2), if_not_exists => true)",
		table, timeColumn,
	)
	if err != nil {
		return fmt.Errorf("timescale: create_hypertable %s: %w", table, err)
	}
	return nil
}

// SetRetention configures a data-retention policy that drops chunks older than
// duration. Call after CreateHypertable.
func (d *DB) SetRetention(ctx context.Context, table string, duration time.Duration) error {
	interval, err := intervalValue(duration)
	if err != nil {
		return fmt.Errorf("timescale: retention duration: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	_, err = d.pool.Exec(
		ctx,
		"SELECT add_retention_policy($1, ($2)::interval, if_not_exists => true)",
		table, interval,
	)
	if err != nil {
		return fmt.Errorf("timescale: add_retention_policy %s: %w", table, err)
	}
	return nil
}

// EnableCompression enables TimescaleDB columnar compression on the hypertable.
func (d *DB) EnableCompression(ctx context.Context, table string) error {
	query, err := compressionSQL(table)
	if err != nil {
		return fmt.Errorf("timescale: enable compression: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	_, err = d.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("timescale: enable compression %s: %w", table, err)
	}
	return nil
}

// AddCompressionPolicy adds an automatic compression policy that compresses
// chunks older than olderThan.
func (d *DB) AddCompressionPolicy(ctx context.Context, table string, olderThan time.Duration) error {
	interval, err := intervalValue(olderThan)
	if err != nil {
		return fmt.Errorf("timescale: compression duration: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	_, err = d.pool.Exec(
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
	if d == nil || d.pool == nil {
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

func compressionSQL(table string) (string, error) {
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
	return "ALTER TABLE " + pgx.Identifier(parts).Sanitize() + " SET (timescaledb.compress)", nil
}
