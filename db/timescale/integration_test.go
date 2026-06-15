package timescale_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/db/timescale"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// TestDB_Lifecycle exercises the full hypertable lifecycle against a real
// TimescaleDB container: create_hypertable, retention, compression enable, and
// the compression policy. These helpers carry no unit coverage because they
// require the TimescaleDB extension to be loaded.
func TestDB_Lifecycle(t *testing.T) {
	t.Parallel()
	pool := pgtest.StartTimescale(t)
	ctx := context.Background()

	mustExec(ctx, t, pool, `CREATE TABLE metrics (time timestamptz NOT NULL, val double precision)`)

	db := timescale.New(pool)

	require.NoError(t, db.CreateHypertable(ctx, "metrics", "time"))
	require.True(t, isHypertable(ctx, t, pool, "metrics"), "metrics should be a hypertable")

	// Idempotent: a second call must not error (if_not_exists => true).
	require.NoError(t, db.CreateHypertable(ctx, "metrics", "time"))

	require.NoError(t, db.SetRetention(ctx, "metrics", 30*24*time.Hour))
	require.NoError(t, db.EnableCompression(ctx, "metrics"))
	require.NoError(t, db.AddCompressionPolicy(ctx, "metrics", 7*24*time.Hour))

	// Both policies must be registered in the TimescaleDB jobs catalog.
	require.True(t, hasJob(ctx, t, pool, "policy_retention"), "retention policy missing")
	require.True(t, hasJob(ctx, t, pool, "policy_compression"), "compression policy missing")
}

// TestDB_RetentionHours covers the sub-day interval branch of formatInterval
// (hours not divisible by 24) end-to-end against TimescaleDB.
func TestDB_RetentionHours(t *testing.T) {
	t.Parallel()
	pool := pgtest.StartTimescale(t)
	ctx := context.Background()

	mustExec(ctx, t, pool, `CREATE TABLE events (time timestamptz NOT NULL, kind text)`)
	db := timescale.New(pool)
	require.NoError(t, db.CreateHypertable(ctx, "events", "time"))
	require.NoError(t, db.SetRetention(ctx, "events", 36*time.Hour))
	require.True(t, hasJob(ctx, t, pool, "policy_retention"))
}

func mustExec(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	_, err := pool.Exec(ctx, sql)
	require.NoError(t, err, "exec: %s", sql)
}

func isHypertable(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	err := pool.QueryRow(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_name = $1)`,
		name,
	).Scan(&ok)
	require.NoError(t, err)
	return ok
}

func hasJob(ctx context.Context, t *testing.T, pool *pgxpool.Pool, proc string) bool {
	t.Helper()
	var ok bool
	err := pool.QueryRow(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM timescaledb_information.jobs WHERE proc_name = $1)`,
		proc,
	).Scan(&ok)
	require.NoError(t, err)
	return ok
}
