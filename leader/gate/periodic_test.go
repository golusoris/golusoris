// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/leaderx at 6111de5a.

package gate_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/leader/gate"
)

const (
	periodicInterval = time.Minute
	// tickBuffer is more ticks than any test here produces, so a tick never blocks the loop.
	tickBuffer = 16
	// stopBound bounds a stop that is expected to succeed (HISS-02).
	stopBound = 5 * time.Second
	// windDown is how long the tick in flight takes to end once it is cancelled: long enough
	// that a Stop which does not wait returns first.
	windDown = 50 * time.Millisecond
)

// recordTicks is a tick function that hands each call's context to the test.
func recordTicks() (<-chan context.Context, func(context.Context) error) {
	calls := make(chan context.Context, tickBuffer)
	return calls, func(ctx context.Context) error {
		calls <- ctx
		return nil
	}
}

func nextTick(t *testing.T, calls <-chan context.Context) context.Context {
	t.Helper()
	select {
	case ctx := <-calls:
		return ctx
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a tick")
		return nil
	}
}

func mustStart(t *testing.T, p *gate.Periodic, parent context.Context) {
	t.Helper()
	if err := p.Start(parent); err != nil {
		t.Fatalf("Start() = %v", err)
	}
}

func mustStop(t *testing.T, p *gate.Periodic) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), stopBound)
	defer cancel()
	if err := p.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	if p.Running() {
		t.Fatal("the loop still runs after Stop")
	}
}

// Positive: the first tick runs at once, then one per interval, each under a deadline of one
// interval; after Stop no tick follows.
func TestPeriodic_TicksAtOnceAndPerInterval(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	calls, tick := recordTicks()
	p := gate.NewPeriodic("job", periodicInterval, clk, &observed{}, tick)
	if p.Running() {
		t.Fatal("a new loop runs before Start")
	}
	mustStart(t, p, t.Context())
	if !p.Running() {
		t.Fatal("the loop does not run after Start")
	}
	first := nextTick(t, calls)
	deadline, ok := first.Deadline()
	if left := time.Until(deadline); !ok || left <= 0 || left > periodicInterval {
		t.Fatalf("tick deadline in %v (set: %v), want within one interval of %v", left, ok, periodicInterval)
	}
	for range 3 {
		clk.Advance(periodicInterval)
		nextTick(t, calls)
	}
	mustStop(t, p)
	clk.Advance(periodicInterval)
	if extra := len(calls); extra != 0 {
		t.Fatalf("%d ticks nobody asked for: want one at the start and one per interval", extra)
	}
}

// Positive: the gate starts a loop again when the replica wins the lease back.
func TestPeriodic_StartsAgainAfterStop(t *testing.T) {
	t.Parallel()
	calls, tick := recordTicks()
	p := gate.NewPeriodic("job", periodicInterval, clock.NewFake(), &observed{}, tick)
	for term := range 3 {
		mustStart(t, p, t.Context())
		nextTick(t, calls)
		mustStop(t, p)
		if extra := len(calls); extra != 0 {
			t.Fatalf("term %d: %d extra ticks, want one tick per start", term, extra)
		}
	}
}

// Negative: a second start while the loop runs is refused and leaves the running loop alone.
func TestPeriodic_SecondStartIsRefused(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	calls, tick := recordTicks()
	p := gate.NewPeriodic("job", periodicInterval, clk, &observed{}, tick)
	mustStart(t, p, t.Context())
	nextTick(t, calls)
	if err := p.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "job: loop is already running") {
		t.Fatalf("second Start() = %v, want an error that names the job and loop already running", err)
	}
	clk.Advance(periodicInterval)
	nextTick(t, calls)
	mustStop(t, p)
	if extra := len(calls); extra != 0 {
		t.Fatalf("%d extra ticks: the refused start launched a second loop", extra)
	}
}

// Negative: a tick that ignores its context outlives the stop's deadline; Stop reports that
// with the job's name and the context's error.
func TestPeriodic_StopDeadlineExpires(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	p := gate.NewPeriodic("job", periodicInterval, clock.NewFake(), &observed{}, func(context.Context) error {
		close(entered)
		<-release
		return nil
	})
	t.Cleanup(func() { close(release) })
	mustStart(t, p, t.Context())
	await(t, entered, "the tick")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := p.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "job: stop: ") {
		t.Fatalf("Stop() = %v, want \"job: stop: \" wrapping the deadline error", err)
	}
}

// Boundary: stopping a loop that never started, or stopping twice, is a no-op that does not
// wait for the context.
func TestPeriodic_StopWithoutRunningLoop(t *testing.T) {
	t.Parallel()
	calls, tick := recordTicks()
	p := gate.NewPeriodic("job", periodicInterval, clock.NewFake(), &observed{}, tick)
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.Stop(ended); err != nil {
		t.Fatalf("Stop() before Start = %v, want nil", err)
	}
	mustStart(t, p, t.Context())
	nextTick(t, calls)
	mustStop(t, p)
	if err := p.Stop(ended); err != nil {
		t.Fatalf("second Stop() = %v, want nil", err)
	}
}

// Boundary: the loop outlives the context it was started with (the gate's is often cancelled
// by the time the loop works), and Stop cancels the tick in flight and waits for it. The tick
// Stop cut short is no failure: the observer never hears of it.
func TestPeriodic_StopCancelsTheTickInFlight(t *testing.T) {
	t.Parallel()
	entered, cancelled, left := make(chan struct{}), make(chan struct{}), make(chan struct{})
	obs := &observed{}
	p := gate.NewPeriodic("job", periodicInterval, clock.NewFake(), obs, func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		time.Sleep(windDown)
		close(left)
		return ctx.Err()
	})
	parent, cancel := context.WithCancel(t.Context())
	mustStart(t, p, parent)
	await(t, entered, "the tick")
	cancel()
	select {
	case <-cancelled:
		t.Fatal("the tick was cancelled with the start context, want it to run until Stop")
	case <-time.After(20 * time.Millisecond):
	}
	mustStop(t, p)
	select {
	case <-left:
	default:
		t.Fatal("Stop returned before the tick in flight ended")
	}
	if got := obs.snapshot(); len(got) != 0 {
		t.Fatalf("the stopped tick was reported: %+v, want no report", got)
	}
}

// observed is an Observer that keeps every reported tick and, when seen is set, hands it to
// the test as well.
type observed struct {
	mu    sync.Mutex
	ticks []observedTick
	seen  chan observedTick
}

type observedTick struct {
	name     string
	interval time.Duration
	at       time.Time
	err      error
}

func (o *observed) LoopTicked(name string, interval time.Duration, at time.Time, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	tick := observedTick{name, interval, at, err}
	o.ticks = append(o.ticks, tick)
	if o.seen != nil {
		o.seen <- tick
	}
}

func (o *observed) snapshot() []observedTick {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]observedTick(nil), o.ticks...)
}

// Positive, negative, boundary: every tick reaches the observer with the loop's name and
// interval, the time it ended and the error it returned; a failed tick does not stop the loop.
func TestPeriodic_ReportsEachTick(t *testing.T) {
	t.Parallel()
	errTick := errors.New("tick failed")
	results := []error{nil, errTick, nil}
	clk := clock.NewFake()
	obs := &observed{seen: make(chan observedTick, tickBuffer)}
	n := 0
	p := gate.NewPeriodic("job", periodicInterval, clk, obs, func(context.Context) error {
		err := results[n%len(results)]
		n++
		return err
	})
	mustStart(t, p, t.Context())
	for i, want := range results {
		if i > 0 {
			clk.Advance(periodicInterval)
		}
		g := receiveTick(t, obs.seen, i)
		verifyTick(t, g, want, i, clk.Now())
	}
	mustStop(t, p)
	if got := len(obs.snapshot()); got != len(results) {
		t.Fatalf("%d ticks reported, want %d", got, len(results))
	}
}

func receiveTick(t *testing.T, seen <-chan observedTick, i int) observedTick {
	t.Helper()
	select {
	case g := <-seen:
		return g
	case <-time.After(stopBound):
		t.Fatalf("tick %d was not reported", i)
		return observedTick{}
	}
}

func verifyTick(t *testing.T, g observedTick, want error, i int, now time.Time) {
	t.Helper()
	if g.name != "job" || g.interval != periodicInterval {
		t.Fatalf("tick %d metadata mismatch: %+v", i, g)
	}
	if !errors.Is(g.err, want) {
		t.Fatalf("tick %d err = %v, want %v", i, g.err, want)
	}
	if !g.at.Equal(now) {
		t.Fatalf("tick %d at %v, want %v", i, g.at, now)
	}
}
