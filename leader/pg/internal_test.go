// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/leader"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Enabled {
		t.Fatal("want Enabled=false")
	}
	if opts.PG.Retry != 2*time.Second {
		t.Fatalf("want PG.Retry=2s, got %v", opts.PG.Retry)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	opts := Options{
		Enabled:  true,
		Name:     "myapp",
		Identity: "pod-1",
		PG:       BackendOptions{Retry: 5 * time.Second},
	}
	got := opts.withDefaults()
	if got.PG.Retry != 5*time.Second {
		t.Fatalf("want Retry preserved as 5s, got %v", got.PG.Retry)
	}
	if got.Name != "myapp" {
		t.Fatalf("want Name preserved as %q, got %q", "myapp", got.Name)
	}
}

func TestKeyFor_stable(t *testing.T) {
	t.Parallel()
	a, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	b, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if a != b {
		t.Fatalf("want keyFor to be deterministic; got %d and %d", a, b)
	}
}

func TestKeyFor_different(t *testing.T) {
	t.Parallel()
	foo, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	bar, err := keyFor("bar")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if foo == bar {
		t.Fatal("want different keys for different names")
	}
}

type probeFunc func(context.Context) error

func (f probeFunc) Exec(ctx context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, f(ctx)
}

func TestHeartbeat_probeErrorEndsTerm(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	calls := 0
	probe := probeFunc(func(context.Context) error {
		calls++
		if calls == 2 {
			return errors.New("terminating connection due to administrator command")
		}
		return nil
	})
	done := make(chan struct{})
	go func() { defer close(done); heartbeat(context.Background(), probe, time.Second, clk) }()
	for range 2 {
		if err := clk.BlockUntilContext(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Second)
	}
	waitClosed(t, done, "heartbeat return after probe error")
	if calls != 2 {
		t.Fatalf("probes = %d, want 2", calls)
	}
}

func TestHeartbeat_ctxEndStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	heartbeat(ctx, probeFunc(func(context.Context) error {
		t.Fatal("probe ran after ctx end")
		return nil
	}), time.Second, clock.NewFake())
}

func TestHeartbeat_probeHasDeadline(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	probe := probeFunc(func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("probe ctx has no deadline")
		}
		return errors.New("stop")
	})
	done := make(chan struct{})
	go func() { defer close(done); heartbeat(context.Background(), probe, time.Second, clk) }()
	if err := clk.BlockUntilContext(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	waitClosed(t, done, "heartbeat return")
}

func TestLeadWhileHeld_lostSessionStopsLeading(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	stopped := make(chan struct{})
	cb := leader.Callbacks{OnStoppedLeading: func() { close(stopped) }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		leadWhileHeld(context.Background(), probeFunc(func(context.Context) error { return errors.New("gone") }), time.Second, clk, "pod-1", cb)
	}()
	if err := clk.BlockUntilContext(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	waitClosed(t, done, "term end after lost session")
	waitClosed(t, stopped, "OnStoppedLeading")
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: none within 5s", what)
	}
}
