// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package migrate_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/log"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

const moduleQueryTimeout = 30 * time.Second

// upCounter counts "up complete" records, one per Migrator.Up call.
type upCounter struct {
	slog.Handler
	n *atomic.Int32
}

func (c upCounter) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "db/migrate: up complete" {
		c.n.Add(1)
	}
	return c.Handler.Handle(ctx, r)
}

// Enabled is true because DiscardHandler would drop the record before Handle.
func (upCounter) Enabled(context.Context, slog.Level) bool { return true }

func countingLogger(n *atomic.Int32) *slog.Logger {
	return slog.New(upCounter{Handler: slog.DiscardHandler, n: n})
}

func moduleOptions(t *testing.T, dsn string, mopts dbmigrate.Options, logger *slog.Logger, extra ...fx.Option) []fx.Option {
	t.Helper()
	cfg, err := config.New(config.Options{EnvPrefix: "MIGRATE_MODULE_TEST_"})
	require.NoError(t, err)
	return append([]fx.Option{
		fx.Supply(cfg, logger, dbpgx.Options{DSN: dsn}),
		dbmigrate.Module,
		fx.Replace(mopts),
	}, extra...)
}

// stepProbeTables counts the step_probe_* tables through an independent connection.
func stepProbeTables(t *testing.T, dsn string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), moduleQueryTimeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	var n int
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name LIKE 'step_probe_%'").Scan(&n))
	return n
}

// #1330: auto must migrate even when nothing requests the *Migrator.
func TestModuleAutoRunsUpWithoutConsumer(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)
	var ups atomic.Int32
	app := fxtest.New(t, moduleOptions(t, dsn, dbmigrate.Options{Auto: true}.WithFS(testMigrationsFS()), countingLogger(&ups))...)
	app.RequireStart()
	app.RequireStop()
	require.Equal(t, 2, stepProbeTables(t, dsn))
	require.EqualValues(t, 1, ups.Load())
}

func TestModuleAutoWithConsumerSharesOneMigrator(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)
	var ups atomic.Int32
	var first, second *dbmigrate.Migrator
	app := fxtest.New(t, moduleOptions(t, dsn, dbmigrate.Options{Auto: true}.WithFS(testMigrationsFS()), countingLogger(&ups),
		fx.Invoke(func(m *dbmigrate.Migrator) { first = m }),
		fx.Invoke(func(m *dbmigrate.Migrator) { second = m }),
	)...)
	app.RequireStart()
	app.RequireStop()
	require.NotNil(t, first)
	require.Same(t, first, second)
	require.Equal(t, 2, stepProbeTables(t, dsn))
	require.EqualValues(t, 1, ups.Load())
}

// Without auto nothing builds a Migrator, so a graph with no DSN and no migrations still starts.
func TestModuleWithoutAutoBuildsNoMigrator(t *testing.T) {
	t.Parallel()
	app := fxtest.New(t, moduleOptions(t, "", dbmigrate.Options{}, log.New(log.Options{}))...)
	app.RequireStart()
	app.RequireStop()
}

func TestModuleAutoWithoutDSNFailsApp(t *testing.T) {
	t.Parallel()
	app := fx.New(append([]fx.Option{fx.NopLogger},
		moduleOptions(t, "", dbmigrate.Options{Auto: true}, log.New(log.Options{}))...)...)
	require.ErrorContains(t, app.Err(), "no DSN")
}
