// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pg_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/leader"
	leaderpg "github.com/golusoris/golusoris/leader/pg"
	"github.com/golusoris/golusoris/testutil/pg"
)

func TestRunRequiresName(t *testing.T) {
	t.Parallel()
	// Use nil pool — Run should fail before touching it because Name is empty.
	err := leaderpg.Run(context.Background(), nil, leaderpg.Options{Enabled: true}, clock.NewFake(), leader.Callbacks{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "leader.name is required") {
		t.Errorf("err = %q", err)
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	o := leaderpg.DefaultOptions()
	if o.PG.Retry != 2*time.Second {
		t.Errorf("Retry = %v", o.PG.Retry)
	}
}

// TestTwoReplicasOneLeader spins two goroutines fighting for the same lock
// against a real Postgres. Exactly one should enter OnStartedLeading.
func TestTwoReplicasOneLeader(t *testing.T) {
	t.Parallel()
	pool := pg.Start(t)
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	var leaders atomic.Int32
	started := make(chan struct{}, 2)
	cb := leader.Callbacks{
		OnStartedLeading: func(_ context.Context) {
			leaders.Add(1)
			started <- struct{}{}
		},
	}

	opts := leaderpg.Options{
		Enabled: true,
		Name:    "test-leader",
		PG:      leaderpg.BackendOptions{Retry: 50 * time.Millisecond},
	}

	// Use a separate pool for replica B so they hold independent sessions
	// (same-pool Acquire would serialize via the pool's max-conns, which
	// could mask the advisory-lock contention we want to test).
	poolB, err := pgxpool.New(ctx, pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatalf("poolB: %v", err)
	}
	t.Cleanup(poolB.Close)

	aCtx, aCancel := context.WithCancel(ctx)
	bCtx, bCancel := context.WithCancel(ctx)
	t.Cleanup(func() { aCancel(); bCancel() })

	go func() { _ = leaderpg.Run(aCtx, pool, opts, clock.NewFake(), cb) }()
	go func() { _ = leaderpg.Run(bCtx, poolB, opts, clock.NewFake(), cb) }()

	// Wait for at least one leader.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no leader started within 5s")
	}
	// Give the loser a moment to try + fail.
	time.Sleep(500 * time.Millisecond)

	if got := leaders.Load(); got != 1 {
		t.Errorf("leaders = %d, want exactly 1", got)
	}
}

func TestNamedModule_coexistsWithModule(t *testing.T) {
	t.Parallel()
	var got struct {
		fx.In
		Sched *leader.Status `name:"sched"`
	}
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte("leader:\n  elections:\n    sched:\n      name: sched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	app := fx.New(fx.NopLogger,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		fx.Provide(func() *pgxpool.Pool { return nil }, func() clock.Clock { return clock.NewFake() }),
		leaderpg.Module(leader.Callbacks{}),
		leaderpg.NamedModule("sched", leader.Callbacks{}),
		fx.Populate(&got),
	)
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if got.Sched == nil || got.Sched.IsLeader() {
		t.Fatalf("named status = %v, want non-nil and not leading", got.Sched)
	}
}

func TestNamedModule_invalidKeyFails(t *testing.T) {
	t.Parallel()
	app := fx.New(fx.NopLogger, leaderpg.NamedModule("gc_sweep", leader.Callbacks{}))
	if err := app.Err(); err == nil || !strings.Contains(err.Error(), "leader/pg: invalid election key") {
		t.Fatalf("err = %v, want invalid election key", err)
	}
}
