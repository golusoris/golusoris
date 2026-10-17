// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/leaderx at 6111de5a.

package gate_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/leader"
	"github.com/golusoris/golusoris/leader/gate"
	leaderpg "github.com/golusoris/golusoris/leader/pg"
	"github.com/golusoris/golusoris/testutil/pg"
)

const defaultTestRetry = 200 * time.Millisecond

// electionBound covers the first election, which also opens the pool connections.
const electionBound = 10 * time.Second

// failoverBound caps a takeover or a loop stop at ten retry periods.
const failoverBound = 10 * defaultTestRetry

// maxSamples bounds the sampler loop: 2000 ticks of 10 ms = 20 s.
const maxSamples = 2000

func openTestPool(t *testing.T, base *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := base.Config().Copy()
	cfg.AfterRelease = func(*pgx.Conn) bool {
		return false
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func pollUntil(t *testing.T, clk clock.Clock, check func() bool, bound time.Duration, what string) time.Duration {
	t.Helper()
	deadline := clk.Now().Add(bound)
	start := clk.Now()
	for i := 0; i < maxSamples && clk.Now().Before(deadline); i++ {
		if check() {
			return clk.Since(start)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if check() {
		return clk.Since(start)
	}
	t.Fatalf("timed out waiting for %s after %v", what, bound)
	return 0
}

func makeElect(pool *pgxpool.Pool, lockName, identity string, clk clock.Clock) gate.Elect {
	opts := leaderpg.Options{
		Enabled:  true,
		Name:     lockName,
		Identity: identity,
		PG:       leaderpg.BackendOptions{Retry: defaultTestRetry},
	}
	return func(ctx context.Context, cb leader.Callbacks) error {
		return leaderpg.Run(ctx, pool, opts, clk, cb)
	}
}

func makeElectPair(poolA, poolB *pgxpool.Pool, nameA, nameB string, clk clock.Clock) (gate.Elect, gate.Elect) {
	return makeElect(poolA, nameA, "replica-a", clk),
		makeElect(poolB, nameB, "replica-b", clk)
}

func startGateRunner(t *testing.T, g *gate.Gate, elect gate.Elect) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := g.Run(ctx, elect, defaultTestRetry); err != nil {
			t.Errorf("gate run: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel
}

type gateSample struct {
	at       time.Time
	runningA bool
	runningB bool
}

type gateSampler struct {
	stop chan struct{}
	done chan struct{}
	mu   sync.Mutex
	data []gateSample
}

func startSampler(clk clock.Clock, gA, gB *gate.Gate) *gateSampler {
	s := &gateSampler{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for range maxSamples {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				s.data = append(s.data, gateSample{
					at:       clk.Now(),
					runningA: gA.Running(),
					runningB: gB.Running(),
				})
				s.mu.Unlock()
			}
		}
	}()
	return s
}

func (s *gateSampler) stopSampling() []gateSample {
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.data)
}

func checkMutualLoopCounts(t *testing.T, runA bool, loopA, loopB *fakeLoop) {
	t.Helper()
	sA, stopA := loopA.counts()
	sB, stopB := loopB.counts()
	if runA {
		if sA != 1 || stopA != 0 || sB != 0 || stopB != 0 {
			t.Fatalf("counts: gateA=%d/%d, gateB=%d/%d, want 1/0 and 0/0", sA, stopA, sB, stopB)
		}
		return
	}
	if sB != 1 || stopB != 0 || sA != 0 || stopA != 0 {
		t.Fatalf("counts: gateB=%d/%d, gateA=%d/%d, want 1/0 and 0/0", sB, stopB, sA, stopA)
	}
}

// Positive: exactly one gate runs its loops after both start; both are healthy.
func TestLeaderFailover_MutualExclusionAndHealth(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping integration test under -short")
	}
	base := pg.Start(t)
	poolA := openTestPool(t, base)
	poolB := openTestPool(t, poolA)

	clk := clockwork.NewRealClock()
	lockName := fmt.Sprintf("lock-%d-%s", os.Getpid(), t.Name())

	loopA := newFakeLoop(nil)
	gateA := gate.New(clk, quiet(), loopA)
	loopB := newFakeLoop(nil)
	gateB := gate.New(clk, quiet(), loopB)

	electA, electB := makeElectPair(poolA, poolB, lockName, lockName, clk)
	startGateRunner(t, gateA, electA)
	startGateRunner(t, gateB, electB)

	pollUntil(t, clk, func() bool {
		return gateA.Running() || gateB.Running()
	}, electionBound, "one gate to become leader")

	time.Sleep(50 * time.Millisecond)

	runA, runB := gateA.Running(), gateB.Running()
	if runA == runB {
		t.Fatalf("running mismatch: gateA=%v, gateB=%v, want exactly one", runA, runB)
	}
	checkMutualLoopCounts(t, runA, loopA, loopB)

	if err := gateA.Check("gateA").Fn(context.Background()); err != nil {
		t.Fatalf("gateA Check: %v", err)
	}
	if err := gateB.Check("gateB").Fn(context.Background()); err != nil {
		t.Fatalf("gateB Check: %v", err)
	}
}

// Positive: canceling leader Run ctx stops leader loops; follower takes over within bound.
func TestLeaderFailover_CleanShutdown(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping integration test under -short")
	}
	base := pg.Start(t)
	poolA := openTestPool(t, base)
	poolB := openTestPool(t, poolA)

	clk := clockwork.NewRealClock()
	lockName := fmt.Sprintf("lock-%d-%s", os.Getpid(), t.Name())

	loopA, loopB := newFakeLoop(nil), newFakeLoop(nil)
	gateA, gateB := gate.New(clk, quiet(), loopA), gate.New(clk, quiet(), loopB)

	electA, electB := makeElectPair(poolA, poolB, lockName, lockName, clk)
	cancelA := startGateRunner(t, gateA, electA)
	cancelB := startGateRunner(t, gateB, electB)

	pollUntil(t, clk, func() bool {
		return gateA.Running() || gateB.Running()
	}, electionBound, "initial leader election")

	leaderGate, followerGate := gateA, gateB
	leaderLoop, followerLoop := loopA, loopB
	cancelLeader := cancelA
	if gateB.Running() {
		leaderGate, followerGate = gateB, gateA
		leaderLoop, followerLoop = loopB, loopA
		cancelLeader = cancelB
	}

	cancelLeader()

	pollUntil(t, clk, func() bool {
		starts, stops := leaderLoop.counts()
		return starts > 0 && starts == stops && !leaderGate.Running()
	}, failoverBound, "leader loops to stop")

	takeover := pollUntil(t, clk, followerGate.Running, failoverBound, "follower loops to start")
	t.Logf("clean shutdown failover takeover: %v", takeover)

	fStarts, fStops := followerLoop.counts()
	if fStarts != 1 || fStops != 0 {
		t.Fatalf("follower loop: starts=%d, stops=%d, want 1/0", fStarts, fStops)
	}
}

// Boundary: session loss causes follower takeover; old leader stops loops on ctx cancel.
func TestLeaderFailover_SessionLoss(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping integration test under -short")
	}
	base := pg.Start(t)
	poolA := openTestPool(t, base)
	poolB := openTestPool(t, poolA)
	adminPool := openTestPool(t, poolA)

	clk := clockwork.NewRealClock()
	lockName := fmt.Sprintf("lock-%d-%s", os.Getpid(), t.Name())

	loopA, loopB := newFakeLoop(nil), newFakeLoop(nil)
	gateA, gateB := gate.New(clk, quiet(), loopA), gate.New(clk, quiet(), loopB)

	electA, electB := makeElectPair(poolA, poolB, lockName, lockName, clk)
	cancelA := startGateRunner(t, gateA, electA)
	cancelB := startGateRunner(t, gateB, electB)

	pollUntil(t, clk, func() bool {
		return gateA.Running() || gateB.Running()
	}, electionBound, "initial leader election")

	leaderGate := gateA
	cancelLeader := cancelA
	leaderIsA := true
	if gateB.Running() {
		leaderGate = gateB
		cancelLeader = cancelB
		leaderIsA = false
	}

	sampler := startSampler(clk, gateA, gateB)
	terminatedAt := clk.Now()
	terminateAdvisoryBackend(t, adminPool)

	takeover := pollUntil(t, clk, func() bool {
		if leaderIsA {
			return gateB.Running()
		}
		return gateA.Running()
	}, failoverBound, "follower takeover")
	t.Logf("session loss failover takeover: %v", takeover)

	time.Sleep(2 * defaultTestRetry)
	cancelLeader()
	lossObservedAt := clk.Now()

	pollUntil(t, clk, func() bool {
		return !leaderGate.Running()
	}, failoverBound, "old leader to stop loops after loss observation")

	samples := sampler.stopSampling()
	overlap := overlapWindow(samples, terminatedAt)
	t.Logf("split-brain window after backend kill: %v", overlap)
	verifyNoOverlapAfterLoss(t, samples, lossObservedAt, leaderIsA)
}

func terminateAdvisoryBackend(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var terminated bool
	err := pool.QueryRow(ctx,
		"SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND granted AND database = (SELECT oid FROM pg_database WHERE datname = current_database())",
	).Scan(&terminated)
	if err != nil || !terminated {
		t.Fatalf("terminate advisory lock backend: terminated=%v, err=%v", terminated, err)
	}
}

func verifyNoOverlapAfterLoss(t *testing.T, samples []gateSample, lossAt time.Time, leaderIsA bool) {
	t.Helper()
	for _, s := range samples {
		if !s.at.After(lossAt) {
			continue
		}
		leaderRunning, followerRunning := s.runningA, s.runningB
		if !leaderIsA {
			leaderRunning, followerRunning = s.runningB, s.runningA
		}
		if leaderRunning && followerRunning {
			t.Fatalf("both gates running after loss observed at sample %v", s.at)
		}
	}
}

func overlapWindow(samples []gateSample, from time.Time) time.Duration {
	var first, last time.Time
	for _, s := range samples {
		if s.at.Before(from) || !s.runningA || !s.runningB {
			continue
		}
		if first.IsZero() {
			first = s.at
		}
		last = s.at
	}
	return last.Sub(first)
}

// Negative: two gates with DIFFERENT lock names both run (proves lock name separates them).
func TestLeaderFailover_DistinctLockNames(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping integration test under -short")
	}
	base := pg.Start(t)
	poolA := openTestPool(t, base)
	poolB := openTestPool(t, poolA)

	clk := clockwork.NewRealClock()

	nameA := fmt.Sprintf("distinct-lock-a-%d-%s", os.Getpid(), t.Name())
	nameB := fmt.Sprintf("distinct-lock-b-%d-%s", os.Getpid(), t.Name())

	loopA, loopB := newFakeLoop(nil), newFakeLoop(nil)
	gateA, gateB := gate.New(clk, quiet(), loopA), gate.New(clk, quiet(), loopB)

	electA, electB := makeElectPair(poolA, poolB, nameA, nameB, clk)
	startGateRunner(t, gateA, electA)
	startGateRunner(t, gateB, electB)

	pollUntil(t, clk, func() bool {
		return gateA.Running() && gateB.Running()
	}, electionBound, "both gates to run on distinct lock names")

	sA, _ := loopA.counts()
	sB, _ := loopB.counts()
	if sA != 1 || sB != 1 {
		t.Fatalf("expected both loops started: sA=%d, sB=%d", sA, sB)
	}
}
