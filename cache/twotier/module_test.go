// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/twotier"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/realtime/pubsub"
)

// twotierOptions boots twotier.Module over an in-memory L1 and the given
// cache.twotier YAML block.
func twotierOptions(t *testing.T, body string, extra ...fx.Option) []fx.Option {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("cache:\n  twotier:\n"+body), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	l1, err := memory.NewForTest(100, 0)
	require.NoError(t, err)
	return append([]fx.Option{
		fx.Supply(cfg, l1),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		twotier.Module,
		fx.NopLogger,
	}, extra...)
}

func TestModule_L1OnlyNeedsNoRedis(t *testing.T) {
	t.Parallel()
	var tt *twotier.TwoTier
	app := fxtest.New(t, twotierOptions(t, "    l2: none\n", fx.Populate(&tt))...)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	view := twotier.NewTyped[string](tt, "user")
	require.NoError(t, view.Set(context.Background(), "1", "ada"))
	got, err := view.Get(context.Background(), "1", func(context.Context) (string, error) { return "loader", nil })
	require.NoError(t, err)
	require.Equal(t, "ada", got)
}

func TestModule_InvalidationOverBus(t *testing.T) {
	t.Parallel()
	bus := pubsub.New()
	var tt *twotier.TwoTier
	app := fxtest.New(t, twotierOptions(t, "    l2: none\n    invalidation:\n      enabled: true\n",
		fx.Supply(fx.Annotate(bus, fx.As(new(pubsub.Bus)))),
		fx.Populate(&tt),
	)...)
	app.RequireStart()
	view := twotier.NewTyped[int](tt, "n")
	require.NoError(t, view.Set(context.Background(), "k", 1))
	bus.Publish(context.Background(), pubsub.Message{
		Topic: twotier.DefaultInvalidationTopic,
		Data:  []byte(`{"origin":"peer","kind":"key","key":"n:k"}`),
	})
	got, err := view.Get(context.Background(), "k", func(context.Context) (int, error) { return 2, nil })
	require.NoError(t, err)
	require.Equal(t, 2, got, "the started module evicts on peer notices")
	app.RequireStop()
	require.NoError(t, view.Set(context.Background(), "k", 3))
	bus.Publish(context.Background(), pubsub.Message{
		Topic: twotier.DefaultInvalidationTopic,
		Data:  []byte(`{"origin":"peer","kind":"key","key":"n:k"}`),
	})
	got, err = view.Get(context.Background(), "k", func(context.Context) (int, error) { return 4, nil })
	require.NoError(t, err)
	require.Equal(t, 3, got, "a stopped module no longer listens")
}

func TestModule_RejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	tests := map[string]struct{ body, want string }{
		"redis l2 without client":  {"    l2: redis\n", "Redis client"},
		"unknown l2":               {"    l2: memcached\n", `unknown l2 "memcached"`},
		"invalidation without bus": {"    l2: none\n    invalidation:\n      enabled: true\n", "needs a pubsub.Bus"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			app := fx.New(twotierOptions(t, tc.body, fx.Invoke(func(*twotier.TwoTier) {}))...)
			require.ErrorContains(t, app.Err(), tc.want)
		})
	}
}

func TestModule_InvalidationNeedsL1TTL(t *testing.T) {
	t.Parallel()
	app := fx.New(twotierOptions(t, "    l2: none\n    l1_ttl: 0s\n    invalidation:\n      enabled: true\n",
		fx.Supply(fx.Annotate(pubsub.New(), fx.As(new(pubsub.Bus)))),
		fx.Invoke(func(*twotier.TwoTier) {}),
	)...)
	require.ErrorContains(t, app.Err(), "positive l1_ttl")
}
