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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgxpool.Pool with TimescaleDB-specific helpers.
type DB struct{ pool *pgxpool.Pool }

// New returns a TimescaleDB helper backed by pool.
// The pool must connect to a TimescaleDB-enabled PostgreSQL instance.
func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// CreateHypertable converts an existing table into a TimescaleDB hypertable
// partitioned on timeColumn. Idempotent: succeeds if the hypertable already exists.
func (d *DB) CreateHypertable(ctx context.Context, table, timeColumn string) error {
	// if_not_exists=true makes this safe to call on every startup.
	_, err := d.pool.Exec(ctx,
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
	_, err := d.pool.Exec(ctx,
		"SELECT add_retention_policy($1, INTERVAL $2, if_not_exists => true)",
		table, formatInterval(duration),
	)
	if err != nil {
		return fmt.Errorf("timescale: add_retention_policy %s: %w", table, err)
	}
	return nil
}

// EnableCompression enables TimescaleDB columnar compression on the hypertable.
func (d *DB) EnableCompression(ctx context.Context, table string) error {
	_, err := d.pool.Exec(ctx,
		fmt.Sprintf("ALTER TABLE %s SET (timescaledb.compress)", table), //nolint:gosec // table name is caller-supplied; not user input
	)
	if err != nil {
		return fmt.Errorf("timescale: enable compression %s: %w", table, err)
	}
	return nil
}

// AddCompressionPolicy adds an automatic compression policy that compresses
// chunks older than olderThan.
func (d *DB) AddCompressionPolicy(ctx context.Context, table string, olderThan time.Duration) error {
	_, err := d.pool.Exec(ctx,
		"SELECT add_compression_policy($1, INTERVAL $2, if_not_exists => true)",
		table, formatInterval(olderThan),
	)
	if err != nil {
		return fmt.Errorf("timescale: add_compression_policy %s: %w", table, err)
	}
	return nil
}

// Pool returns the underlying pgxpool.Pool.
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// formatInterval converts a Go duration to a PostgreSQL interval string
// suitable for TimescaleDB policy calls.
func formatInterval(d time.Duration) string {
	hours := int(d.Hours())
	if hours%24 == 0 {
		return fmt.Sprintf("%d days", hours/24)
	}
	return fmt.Sprintf("%d hours", hours)
}
