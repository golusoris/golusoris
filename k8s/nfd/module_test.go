// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nfd_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/k8s/nfd"
)

var epoch = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func newConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	return cfg
}

func newApp(t *testing.T, cfg *config.Config, clk clock.Clock, src nfd.Source) *fxtest.App {
	t.Helper()
	return fxtest.New(t,
		fx.NopLogger,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		fx.Provide(func() clock.Clock { return clk }),
		nfd.Module(src),
	)
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := nfd.DefaultOptions()
	require.False(t, o.Enabled)
	require.Equal(t, nfd.DefaultDir, o.Dir)
	require.Equal(t, 10*time.Minute, o.TTL)
	require.Equal(t, 2*time.Minute, o.Refresh)
	require.Equal(t, 10*time.Second, o.Timeout)
}

func TestNewWriter_validates(t *testing.T) {
	t.Parallel()
	ok := nfd.DefaultOptions()
	ok.Name = "f"
	cases := map[string]func(o *nfd.Options){
		"missing name":       func(o *nfd.Options) { o.Name = "" },
		"dot name":           func(o *nfd.Options) { o.Name = ".f" },
		"empty dir":          func(o *nfd.Options) { o.Dir = "" },
		"zero refresh":       func(o *nfd.Options) { o.Refresh = 0 },
		"zero timeout":       func(o *nfd.Options) { o.Timeout = 0 },
		"ttl equals refresh": func(o *nfd.Options) { o.TTL = o.Refresh },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := ok
			mutate(&o)
			_, err := nfd.NewWriter(o, nfd.Static(nil), clock.NewFake())
			require.Error(t, err)
		})
	}
	_, err := nfd.NewWriter(ok, nil, clock.NewFake())
	require.ErrorContains(t, err, "source and clock are required")

	o := ok
	o.TTL = o.Refresh + time.Nanosecond
	_, err = nfd.NewWriter(o, nfd.Static(nil), clock.NewFake())
	require.NoError(t, err)
}

func TestStatic_copiesLabels(t *testing.T) {
	t.Parallel()
	in := map[string]string{"example.com/a": "b"}
	src := nfd.Static(in)
	in["example.com/a"] = "mutated"
	got, err := src(context.Background())
	require.NoError(t, err)
	got["example.com/x"] = "y"
	again, err := src(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]string{"example.com/a": "b"}, again)
}

func TestWriter_Refresh_writesExpiryFromClock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	o := nfd.DefaultOptions()
	o.Dir, o.Name = dir, "f"
	w, err := nfd.NewWriter(o, nfd.Static(map[string]string{"example.com/a": "b"}), clockwork.NewFakeClockAt(epoch))
	require.NoError(t, err)
	require.NoError(t, w.Refresh(context.Background()))

	got, err := os.ReadFile(filepath.Join(dir, "f"))
	require.NoError(t, err)
	require.Equal(t, "# +expiry-time=2026-10-07T12:10:00Z\nexample.com/a=b\n", string(got))
}

func TestWriter_Refresh_sourceErrorKeepsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	o := nfd.DefaultOptions()
	o.Dir, o.Name = dir, "f"
	require.NoError(t, nfd.WriteFeatureFile(dir, "f", map[string]string{"example.com/a": "old"}))
	w, err := nfd.NewWriter(o, func(context.Context) (map[string]string, error) {
		return nil, errors.New("probe failed")
	}, clock.NewFake())
	require.NoError(t, err)
	require.ErrorContains(t, w.Refresh(context.Background()), "nfd: source: probe failed")
	got, err := os.ReadFile(filepath.Join(dir, "f"))
	require.NoError(t, err)
	require.Equal(t, "example.com/a=old\n", string(got))
}

func TestModule_disabledWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := newConfig(t, "k8s:\n  nfd:\n    dir: "+dir+"\n    name: f\n")
	app := newApp(t, cfg, clock.NewFake(), nfd.Static(map[string]string{"example.com/a": "b"}))
	app.RequireStart().RequireStop()
	assertEmptyDir(t, dir)
}

func TestModule_invalidOptionsFailConstruction(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t, "k8s:\n  nfd:\n    enabled: true\n    name: .hidden\n")
	app := fx.New(
		fx.NopLogger,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		nfd.Module(nfd.Static(nil)),
	)
	require.ErrorIs(t, app.Err(), nfd.ErrInvalidName)
}

func TestModule_startFailsWhenFirstWriteFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "absent")
	cfg := newConfig(t, "k8s:\n  nfd:\n    enabled: true\n    dir: "+dir+"\n    name: f\n")
	app := fx.New(
		fx.NopLogger,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		nfd.Module(nfd.Static(nil)),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.ErrorContains(t, app.Start(ctx), "nfd: create temp file")
}

func TestModule_refreshMovesExpiry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := newConfig(t, "k8s:\n  nfd:\n    enabled: true\n    dir: "+dir+"\n    name: f\n    ttl: 3m\n    refresh: 1m\n")
	clk := clockwork.NewFakeClockAt(epoch)
	var calls atomic.Int32
	src := func(context.Context) (map[string]string, error) {
		calls.Add(1)
		return map[string]string{"example.com/a": "b"}, nil
	}
	app := newApp(t, cfg, clk, src)
	app.RequireStart()
	requireExpiry(t, dir, "2026-10-07T12:03:00Z")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	clk.Advance(time.Minute)
	require.Eventually(t, func() bool { return calls.Load() >= 2 }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, clk.BlockUntilContext(ctx, 1))
	requireExpiry(t, dir, "2026-10-07T12:04:00Z")

	app.RequireStop()
	got := calls.Load()
	clk.Advance(time.Minute)
	require.Equal(t, got, calls.Load(), "refresh ran after stop")
}

func requireExpiry(t *testing.T, dir, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "f"))
	require.NoError(t, err)
	first, _, _ := strings.Cut(string(b), "\n")
	require.Equal(t, "# +expiry-time="+want, first)
}
