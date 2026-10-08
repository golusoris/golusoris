// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package always_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/leader"
	"github.com/golusoris/golusoris/leader/always"
	"github.com/golusoris/golusoris/leader/internal/hook"
)

// recorder collects callback events; callbacks run on the elector goroutine.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *recorder) callbacks(prefix string) leader.Callbacks {
	return leader.Callbacks{
		OnNewLeader:      func(id string) { r.add(prefix + "new:" + id) },
		OnStartedLeading: func(context.Context) { r.add(prefix + "started") },
		OnStoppedLeading: func() { r.add(prefix + "stopped") },
	}
}

func newConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	return cfg
}

func ctx5s(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	require.Equal(t, always.Options{}, always.DefaultOptions())
}

func TestRun_firesEachCallbackOnceAndBlocksUntilCancel(t *testing.T) {
	t.Parallel()
	var r recorder
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- always.Run(ctx, always.Options{Identity: "solo"}, r.callbacks("")) }()

	require.Eventually(t, func() bool { return len(r.get()) == 2 }, 5*time.Second, time.Millisecond)
	select {
	case <-done:
		t.Fatal("Run returned before cancel")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	require.NoError(t, <-done)
	require.Equal(t, []string{"new:solo", "started", "stopped"}, r.get())
}

func TestRun_defaultIdentityIsHostname(t *testing.T) {
	t.Parallel()
	var r recorder
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, always.Run(ctx, always.Options{}, r.callbacks("")))
	require.Equal(t, "new:"+hook.Identity(""), r.get()[0])
}

func TestModule_leadsFromStartToStop(t *testing.T) {
	t.Parallel()
	var r recorder
	app := fx.New(fx.NopLogger,
		fx.Supply(newConfig(t, "leader:\n  enabled: true\n  identity: solo\n"), slog.New(slog.DiscardHandler)),
		always.Module(r.callbacks("")),
	)
	require.NoError(t, app.Start(ctx5s(t)))
	require.Eventually(t, func() bool { return len(r.get()) == 2 }, 5*time.Second, time.Millisecond)
	require.NoError(t, app.Stop(ctx5s(t)))
	require.Equal(t, []string{"new:solo", "started", "stopped"}, r.get())
}

func TestModule_disabledNeverLeads(t *testing.T) {
	t.Parallel()
	var r recorder
	app := fx.New(fx.NopLogger,
		fx.Supply(newConfig(t, "leader:\n  identity: solo\n"), slog.New(slog.DiscardHandler)),
		always.Module(r.callbacks("")),
	)
	require.NoError(t, app.Start(ctx5s(t)))
	require.NoError(t, app.Stop(ctx5s(t)))
	require.Empty(t, r.get())
}

func TestNamedModule_independentElectionsWithStatus(t *testing.T) {
	t.Parallel()
	var r recorder
	var got struct {
		fx.In
		Sched *leader.Status `name:"sched"`
		GC    *leader.Status `name:"gc"`
	}
	cfg := newConfig(t, "leader:\n  enabled: true\n  identity: main\n"+
		"  elections:\n    sched:\n      enabled: true\n      identity: s\n    gc:\n      enabled: false\n")
	app := fx.New(fx.NopLogger,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		always.Module(r.callbacks("main/")),
		always.NamedModule("sched", r.callbacks("sched/")),
		always.NamedModule("gc", r.callbacks("gc/")),
		fx.Populate(&got),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(ctx5s(t)))
	require.Eventually(t, func() bool { return len(r.get()) == 4 }, 5*time.Second, time.Millisecond)
	require.Eventually(t, got.Sched.IsLeader, 5*time.Second, time.Millisecond)
	require.False(t, got.GC.IsLeader(), "disabled election never leads")
	require.ElementsMatch(t, []string{"main/new:main", "main/started", "sched/new:s", "sched/started"}, r.get())

	require.NoError(t, app.Stop(ctx5s(t)))
	require.False(t, got.Sched.IsLeader())
	require.Contains(t, r.get(), "sched/stopped")
}

func TestNamedModule_invalidKeyFails(t *testing.T) {
	t.Parallel()
	app := fx.New(fx.NopLogger,
		fx.Supply(newConfig(t, "leader: {}\n"), slog.New(slog.DiscardHandler)),
		always.NamedModule("Bad-Key", leader.Callbacks{}),
	)
	require.ErrorIs(t, app.Err(), hook.ErrInvalidKey)
}
