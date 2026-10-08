// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/db/timescale"
	"github.com/golusoris/golusoris/internal/testimages"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// TestCommunityEdition covers the TSL features end to end: chunk interval,
// compression settings, continuous aggregate + policy + refresh, drop_chunks.
func TestCommunityEdition(t *testing.T) {
	t.Parallel()
	pool := pgtest.StartTimescale(t)
	ctx := context.Background()
	db := timescale.New(pool)

	caps, err := db.Capabilities(ctx)
	require.NoError(t, err)
	require.Equal(t, timescale.EditionCommunity, caps.Edition)
	require.Equal(t, "timescale", caps.License)
	require.NotEmpty(t, caps.Version)

	mustExec(ctx, t, pool, `CREATE TABLE readings (time timestamptz NOT NULL, dev text, val double precision)`)
	require.NoError(t, db.CreateHypertableWithOptions(ctx, "readings", "time",
		timescale.HypertableOptions{ChunkTimeInterval: 6 * time.Hour}))
	require.Equal(t, "06:00:00", chunkInterval(ctx, t, pool, "readings"))
	// Existing hypertable: the new interval applies to future chunks.
	require.NoError(t, db.CreateHypertableWithOptions(ctx, "readings", "time",
		timescale.HypertableOptions{ChunkTimeInterval: 12 * time.Hour}))
	require.Equal(t, "12:00:00", chunkInterval(ctx, t, pool, "readings"))

	require.NoError(t, db.EnableCompressionWithOptions(ctx, "readings", timescale.CompressionOptions{
		SegmentBy: []string{"dev"},
		OrderBy:   []timescale.OrderColumn{{Column: "time", Descending: true}},
	}))
	var segmentBy, orderBy string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT segmentby, orderby FROM timescaledb_information.hypertable_compression_settings
		 WHERE hypertable = 'readings'::regclass`).Scan(&segmentBy, &orderBy))
	require.Equal(t, "dev", segmentBy)
	require.Equal(t, `"time" DESC`, orderBy)

	mustExec(ctx, t, pool, `INSERT INTO readings
		SELECT now() - make_interval(hours => g), 'd1', g FROM generate_series(1, 48) g`)
	exerciseContinuousAggregate(ctx, t, db, pool)

	mustExec(ctx, t, pool, `INSERT INTO readings VALUES (now() - interval '90 days', 'd1', 1)`)
	dropped, err := db.DropChunks(ctx, "readings", 30*24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, 1, dropped)
}

func exerciseContinuousAggregate(ctx context.Context, t *testing.T, db *timescale.DB, pool *pgxpool.Pool) {
	t.Helper()
	agg := timescale.ContinuousAggregate{
		Name:  "readings_hourly",
		Query: `SELECT time_bucket('1 hour', time) AS bucket, dev, avg(val) AS avg_val FROM readings GROUP BY 1, 2`,
	}
	require.NoError(t, db.CreateContinuousAggregate(ctx, agg))
	require.NoError(t, db.CreateContinuousAggregate(ctx, agg), "IF NOT EXISTS keeps creation idempotent")

	policy := timescale.RefreshPolicy{StartOffset: 7 * 24 * time.Hour, EndOffset: time.Hour, ScheduleInterval: time.Hour}
	require.NoError(t, db.AddContinuousAggregatePolicy(ctx, "readings_hourly", policy))
	require.NoError(t, db.AddContinuousAggregatePolicy(ctx, "readings_hourly", policy))
	require.True(t, hasJob(ctx, t, pool, "policy_refresh_continuous_aggregate"))

	require.NoError(t, db.RefreshContinuousAggregate(ctx, "readings_hourly", time.Time{}, time.Time{}))
	var buckets int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM readings_hourly`).Scan(&buckets))
	require.GreaterOrEqual(t, buckets, 47)
}

// TestApacheEdition proves the Apache-only build degrades: hypertables and
// drop_chunks work, every TSL feature returns ErrUnsupportedEdition.
func TestApacheEdition(t *testing.T) {
	t.Parallel()
	pool := pgtest.StartTimescale(t, pgtest.Options{Image: testimages.TimescaleApache})
	ctx := context.Background()
	db := timescale.New(pool)

	caps, err := db.Capabilities(ctx)
	require.NoError(t, err)
	require.Equal(t, timescale.EditionApache, caps.Edition)
	require.True(t, caps.Supports(timescale.FeatureHypertable))
	require.False(t, caps.Supports(timescale.FeatureCompression))

	mustExec(ctx, t, pool, `CREATE TABLE events (time timestamptz NOT NULL, kind text)`)
	require.NoError(t, db.CreateHypertableWithOptions(ctx, "events", "time",
		timescale.HypertableOptions{ChunkTimeInterval: 24 * time.Hour}))
	require.Equal(t, "1 day", chunkInterval(ctx, t, pool, "events"))
	_, err = db.DropChunks(ctx, "events", 30*24*time.Hour)
	require.NoError(t, err)

	for name, call := range map[string]func() error{
		"compression":        func() error { return db.EnableCompression(ctx, "events") },
		"compression policy": func() error { return db.AddCompressionPolicy(ctx, "events", time.Hour) },
		"retention policy":   func() error { return db.SetRetention(ctx, "events", time.Hour) },
		"continuous aggregate": func() error {
			return db.CreateContinuousAggregate(ctx, timescale.ContinuousAggregate{
				Name: "events_hourly", Query: `SELECT time_bucket('1 hour', time), count(*) FROM events GROUP BY 1`,
			})
		},
	} {
		require.ErrorIs(t, call(), timescale.ErrUnsupportedEdition, name)
	}

	require.Error(t, startModule(t, pool, timescale.EditionCommunity))
	require.NoError(t, startModule(t, pool, timescale.EditionApache))
}

// TestWithoutExtension proves plain PostgreSQL reports EditionNone and keeps
// tables plain.
func TestWithoutExtension(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	ctx := context.Background()
	db := timescale.New(pool)

	caps, err := db.Capabilities(ctx)
	require.NoError(t, err)
	require.Equal(t, timescale.EditionNone, caps.Edition)
	require.False(t, caps.Installed())

	mustExec(ctx, t, pool, `CREATE TABLE plain (time timestamptz NOT NULL)`)
	err = db.CreateHypertable(ctx, "plain", "time")
	require.ErrorIs(t, err, timescale.ErrExtensionMissing)
	require.False(t, errors.Is(err, timescale.ErrUnsupportedEdition))

	require.ErrorIs(t, startModule(t, pool, timescale.EditionApache), timescale.ErrExtensionMissing)
	require.NoError(t, startModule(t, pool, ""))
}

func startModule(t *testing.T, pool *pgxpool.Pool, edition timescale.Edition) error {
	t.Helper()
	cfg, err := config.New(config.Options{EnvPrefix: "GOLUSORIS_TIMESCALE_TEST_UNSET_"})
	require.NoError(t, err)
	app := fxtest.New(t,
		fx.Supply(pool, cfg, slog.New(slog.DiscardHandler)),
		timescale.Module,
		fx.Decorate(func(opts timescale.Options) timescale.Options {
			opts.Require = edition
			return opts
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	startErr := app.Start(ctx)
	if startErr == nil {
		require.NoError(t, app.Stop(ctx))
	}
	return startErr
}

func chunkInterval(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table string) string {
	t.Helper()
	var interval string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT time_interval::text FROM timescaledb_information.dimensions WHERE hypertable_name = $1`,
		table).Scan(&interval))
	return interval
}
