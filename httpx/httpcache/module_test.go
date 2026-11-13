// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bartventer/httpcache/store"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/core/config"
)

func moduleOptions(t *testing.T, body string) []fx.Option {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache.yaml")
	require.NoError(t, os.WriteFile(path, []byte("http:\n  outbound:\n    cache:\n"+body), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	l1, err := memory.NewForTest(100, 0)
	require.NoError(t, err)
	return []fx.Option{fx.Supply(cfg, l1), fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }), Module, fx.NopLogger}
}

func TestModuleL1Only(t *testing.T) {
	t.Parallel()
	var c *Cache
	app := fxtest.New(t, append(moduleOptions(t, "      store:\n        l2: none\n"), fx.Populate(&c))...)
	app.RequireStart()
	require.NotNil(t, c)
	require.Equal(t, "httpcache", c.opts.Prefix)
	require.EqualValues(t, 1<<20, c.opts.MaxBodyBytes)
	app.RequireStop()
	_, err := store.Open(driverScheme + "://" + c.id)
	require.Error(t, err)
}

func TestModuleMissingRedisAndInvalidConfig(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "      max_in_flight: -1\n", "      store:\n        l2: unknown\n", "      max_body_bytes: invalid\n"} {
		app := fx.New(moduleOptions(t, body)...)
		require.Error(t, app.Err())
	}
}
