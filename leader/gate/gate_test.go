// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/leaderx at 6111de5a.

package gate_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/leader"
	"github.com/golusoris/golusoris/leader/gate"
)

type fakeLoop struct {
	mu       sync.Mutex
	starts   int
	stops    int
	startErr error
	started  chan struct{}
}

func newFakeLoop(startErr error) *fakeLoop {
	return &fakeLoop{startErr: startErr, started: make(chan struct{}, 1)}
}

func (f *fakeLoop) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	select {
	case f.started <- struct{}{}:
	default:
	}
	return f.startErr
}

func (f *fakeLoop) Stop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	return nil
}

func (f *fakeLoop) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

const awaitBound = 5 * time.Second

func await(tb testing.TB, ch <-chan struct{}, what string) {
	tb.Helper()
	select {
	case <-ch:
	case <-time.After(awaitBound):
		tb.Fatalf("timed out waiting for %s", what)
	}
}

// Positive: without an elector the loops run from start until the context ends.
func TestRun_NoElector(t *testing.T) {
	t.Parallel()
	loop := newFakeLoop(nil)
	g := gate.New(clock.NewFake(), quiet(), loop)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := g.Run(ctx, nil, 0); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	await(t, loop.started, "loop start")
	if !g.Running() {
		t.Fatal("gate should be running")
	}
	cancel()
	await(t, done, "Run to return")
	if starts, stops := loop.counts(); starts != 1 || stops != 1 {
		t.Fatalf("starts=%d stops=%d, want 1/1", starts, stops)
	}
	if g.Running() {
		t.Fatal("gate should have stopped")
	}
}

// Negative: a loop that fails to start is skipped, never stopped, and does not block others.
func TestRun_LoopStartError(t *testing.T) {
	t.Parallel()
	broken := newFakeLoop(errors.New("boom"))
	fine := newFakeLoop(nil)
	g := gate.New(clock.NewFake(), quiet(), broken, fine)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = g.Run(ctx, nil, 0)
	}()
	await(t, fine.started, "second loop start")
	cancel()
	await(t, done, "Run to return")
	if _, stops := broken.counts(); stops != 0 {
		t.Fatalf("broken loop stopped %d times, want 0", stops)
	}
	if starts, stops := fine.counts(); starts != 1 || stops != 1 {
		t.Fatalf("fine loop starts=%d stops=%d, want 1/1", starts, stops)
	}
}

// Boundary: with an elector the loops follow the lease exactly once per term.
func TestRun_FollowsLease(t *testing.T) {
	t.Parallel()
	loop := newFakeLoop(nil)
	g := gate.New(clock.NewFake(), quiet(), loop)
	elect := func(ctx context.Context, cb leader.Callbacks) error {
		cb.OnNewLeader("me")
		cb.OnStartedLeading(ctx)
		<-ctx.Done()
		cb.OnStoppedLeading()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := g.Run(ctx, elect, time.Second); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	await(t, loop.started, "leadership")
	cancel()
	await(t, done, "Run to return")
	if starts, stops := loop.counts(); starts != 1 || stops != 1 {
		t.Fatalf("starts=%d stops=%d, want 1/1 (stopAll must be idempotent)", starts, stops)
	}
}

// Boundary: a failed election is retried after the retry interval, never in a tight loop.
func TestRun_RetriesAfterElectionError(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	loop := newFakeLoop(nil)
	g := gate.New(clk, quiet(), loop)
	attempts := make(chan int, 4)
	n := 0
	elect := func(ctx context.Context, _ leader.Callbacks) error {
		n++
		attempts <- n
		if n == 1 {
			return errors.New("pg down")
		}
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = g.Run(ctx, elect, time.Second)
	}()
	if got := <-attempts; got != 1 {
		t.Fatalf("first attempt = %d", got)
	}
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("BlockUntilContext: %v", err)
	}
	clk.Advance(time.Second)
	if got := <-attempts; got != 2 {
		t.Fatalf("second attempt = %d", got)
	}
	cancel()
	await(t, done, "Run to return")
	if starts, _ := loop.counts(); starts != 0 {
		t.Fatalf("loops started %d times without leadership", starts)
	}
}

// Positive: Callbacks().OnStoppedLeading stops loops and clears running flag on leadership loss.
func TestCallbacks_StopOnLoss(t *testing.T) {
	t.Parallel()
	loop := newFakeLoop(nil)
	g := gate.New(clock.NewFake(), quiet(), loop)

	ctx := t.Context()

	cb := g.Callbacks(ctx)
	cb.OnStartedLeading(ctx)

	if !g.Running() {
		t.Fatal("gate should be running after OnStartedLeading")
	}
	starts, stops := loop.counts()
	if starts != 1 || stops != 0 {
		t.Fatalf("loop counts before stop: starts=%d, stops=%d, want 1/0", starts, stops)
	}

	cb.OnStoppedLeading()

	if g.Running() {
		t.Fatal("gate should not be running after OnStoppedLeading")
	}
	starts, stops = loop.counts()
	if starts != 1 || stops != 1 {
		t.Fatalf("loop counts after stop: starts=%d, stops=%d, want 1/1", starts, stops)
	}
}
