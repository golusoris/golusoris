// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/grpc"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/idempotency"
)

// newConfig loads an idempotency YAML block the way apps configure it.
func newConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("idempotency:\n"+body), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	return cfg
}

func moduleOptions(cfg *config.Config, extra ...fx.Option) []fx.Option {
	return append([]fx.Option{
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		idempotency.Module,
	}, extra...)
}

func TestModule_SelectsSQLiteBackend(t *testing.T) {
	t.Parallel()
	db := openSQLite(t)
	var store idempotency.Store
	app := fxtest.New(t, moduleOptions(
		newConfig(t, "  store: sqlite\n"),
		fx.Provide(func() *sql.DB { return db }),
		fx.Populate(&store),
	)...)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.IsType(t, &idempotency.SQLiteStore{}, store)
	acquire(t, store, "module-sqlite")
}

func TestModule_RejectsMissingOrUnknownBackend(t *testing.T) {
	t.Parallel()
	tests := map[string]struct{ body, want string }{
		"postgres without pool":  {"  store: postgres\n", "needs a *pgxpool.Pool"},
		"redis without client":   {"  store: redis\n", "needs a rueidis.Client"},
		"sqlite without db":      {"  store: sqlite\n", "needs a *sql.DB"},
		"unknown backend":        {"  store: etcd\n", `unknown store "etcd"`},
		"negative sweep":         {"  sweep:\n    interval: -1s\n", "must not be negative"},
		"sweep batch over bound": {"  sweep:\n    batch: 10001\n", "batch in 1..10000"},
		"sweep batch zero":       {"  sweep:\n    batch: 0\n", "batch in 1..10000"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			app := fx.New(append(moduleOptions(newConfig(t, tc.body)),
				fx.Invoke(func(idempotency.Store) {}), fx.NopLogger)...)
			require.ErrorContains(t, app.Err(), tc.want)
		})
	}
}

func TestModule_SweepDisabledAtZeroInterval(t *testing.T) {
	t.Parallel()
	var cfg idempotency.Config
	app := fxtest.New(t, moduleOptions(
		newConfig(t, "  sweep:\n    interval: 0s\n    batch: 0\n"),
		fx.Populate(&cfg),
	)...)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.Zero(t, cfg.Sweep.Interval)
}

func TestGRPCModule_ProvidesServerOption(t *testing.T) {
	t.Parallel()
	var options []grpc.ServerOption
	app := fxtest.New(t, moduleOptions(
		newConfig(t, "  ttl: 1h\n"),
		idempotency.GRPCModule,
		fx.Invoke(fx.Annotate(func(opts []grpc.ServerOption) { options = opts }, fx.ParamTags(`group:"grpc.serveropts"`))),
	)...)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.Len(t, options, 1)
}
