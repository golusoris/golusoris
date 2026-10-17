// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/leaderx at 6111de5a.

package gate_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/leader"
	"github.com/golusoris/golusoris/leader/gate"
)

// runGate runs g in the background and returns a stop function that waits for Run.
func runGate(t *testing.T, g *gate.Gate, elect gate.Elect) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = g.Run(ctx, elect, time.Second)
	}()
	return func() {
		cancel()
		await(t, done, "Run to return")
	}
}

func wantHealth(t *testing.T, g *gate.Gate, want string) {
	t.Helper()
	err := g.Health()
	switch {
	case want == "" && err != nil:
		t.Fatalf("Health = %v, want nil", err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
		t.Fatalf("Health = %v, want an error containing %q", err, want)
	}
}

// Negative: a gate that never ran, or whose Run returned, is not ready.
func TestHealth_NotRunning(t *testing.T) {
	t.Parallel()
	loop := newFakeLoop(nil)
	g := gate.New(clock.NewFake(), quiet(), loop)
	if err := g.Health(); !errors.Is(err, gate.ErrNotRunning) {
		t.Fatalf("before Run: Health = %v, want ErrNotRunning", err)
	}
	stop := runGate(t, g, nil)
	await(t, loop.started, "loop start")
	wantHealth(t, g, "")
	stop()
	if err := g.Health(); !errors.Is(err, gate.ErrNotRunning) {
		t.Fatalf("after Run: Health = %v, want ErrNotRunning", err)
	}
}

// Negative: a loop that failed to start makes the term unhealthy and is named.
func TestHealth_LoopStartFailed(t *testing.T) {
	t.Parallel()
	broken := gate.NewPeriodic("planner", time.Minute, clock.NewFake(), &observed{}, func(context.Context) error { return nil })
	if err := broken.Start(context.Background()); err != nil { // a second start fails
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = broken.Stop(context.Background()) })
	fine := newFakeLoop(nil)
	g := gate.New(clock.NewFake(), quiet(), broken, fine)
	stop := runGate(t, g, nil)
	defer stop()
	await(t, fine.started, "second loop start")
	wantHealth(t, g, "loops failed to start: planner")
}

// Positive: a replica that waits for the lock with a working election is ready.
func TestHealth_Follower(t *testing.T) {
	t.Parallel()
	electing := make(chan struct{})
	elect := func(ctx context.Context, _ leader.Callbacks) error {
		close(electing)
		<-ctx.Done()
		return nil
	}
	g := gate.New(clock.NewFake(), quiet(), newFakeLoop(nil))
	stop := runGate(t, g, elect)
	defer stop()
	await(t, electing, "election")
	wantHealth(t, g, "")
}

// Boundary: a failed election counts while the gate waits to retry and for the first retry
// interval of the next attempt; an attempt that lasts one interval clears it.
func TestHealth_ElectionFailing(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	attempts := make(chan struct{}, 2)
	n := 0
	elect := func(ctx context.Context, _ leader.Callbacks) error {
		n++
		attempts <- struct{}{}
		if n == 1 {
			return errors.New("pg down")
		}
		<-ctx.Done()
		return nil
	}
	g := gate.New(clk, quiet(), newFakeLoop(nil))
	stop := runGate(t, g, elect)
	defer stop()
	await(t, attempts, "first election")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("BlockUntilContext: %v", err)
	}
	wantHealth(t, g, "election failing: pg down")
	clk.Advance(time.Second)
	await(t, attempts, "second election")
	wantHealth(t, g, "election failing: pg down")
	clk.Advance(time.Second)
	wantHealth(t, g, "")
}

// Boundary: a term that starts after a failed election clears the failure at once.
func TestHealth_TermClearsElectionError(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	loop := newFakeLoop(nil)
	n := 0
	elect := func(ctx context.Context, cb leader.Callbacks) error {
		n++
		if n == 1 {
			return errors.New("pg down")
		}
		cb.OnStartedLeading(ctx)
		<-ctx.Done()
		cb.OnStoppedLeading()
		return nil
	}
	g := gate.New(clk, quiet(), loop)
	stop := runGate(t, g, elect)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("BlockUntilContext: %v", err)
	}
	clk.Advance(time.Second)
	await(t, loop.started, "leadership")
	wantHealth(t, g, "")
}
