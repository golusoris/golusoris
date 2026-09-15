// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package search_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/search"
)

// hold is how long a probe backend stays inside Search. Long enough that
// concurrently admitted backends overlap, short enough to keep the suite fast.
const hold = 5 * time.Millisecond

// concurrencyProbe records how many probeSearcher calls were ever in flight at
// the same moment. The peak is decided in both directions: the semaphore in
// MultiSearcher.fanOut guarantees it can never exceed the configured limit
// whatever the scheduler does, and every admitted backend parks in time.Sleep
// rather than spinning, so a fan-out that admits two backends is observed as a
// peak of two on any scheduler — including GOMAXPROCS=1.
type concurrencyProbe struct {
	mu       sync.Mutex
	inFlight int
	peak     int
	calls    int
}

func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.inFlight++
	if p.inFlight > p.peak {
		p.peak = p.inFlight
	}
}

func (p *concurrencyProbe) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight--
}

func (p *concurrencyProbe) snapshot() (calls, peak int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.peak
}

// probeSearcher is a Searcher that only reports its own concurrency.
type probeSearcher struct{ probe *concurrencyProbe }

func (s probeSearcher) Search(_ context.Context, _ string, _ search.Query) (search.Results, error) {
	s.probe.enter()
	defer s.probe.leave()
	time.Sleep(hold)
	return search.Results{}, nil
}

// probeBackends builds n backends sharing one probe.
func probeBackends(t *testing.T, n int) (*concurrencyProbe, []search.Searcher) {
	t.Helper()
	probe := &concurrencyProbe{}
	backends := make([]search.Searcher, 0, n)
	for range n {
		backends = append(backends, probeSearcher{probe: probe})
	}
	return probe, backends
}

// runFanOut queries m once and returns the observed call count and peak.
func runFanOut(t *testing.T, probe *concurrencyProbe, m *search.MultiSearcher) (calls, peak int) {
	t.Helper()
	if _, err := m.Search(context.Background(), "c", search.Query{Q: "q"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	return probe.snapshot()
}

// Positive: an explicit bound caps the live backends while still querying all,
// and the fan-out actually fills that bound. Both directions are asserted: an
// implementation that ignored the limit fails the ceiling on every attempt, and
// one that regressed to querying the backends one after another never reaches
// it. The ceiling is a guarantee of the semaphore and is checked on each
// attempt; reaching it is a scheduling observation, so a stalled attempt is
// retried rather than failed.
func TestMultiSearcher_MaxFanOutCapsConcurrency(t *testing.T) {
	t.Parallel()

	const backends, limit, attempts = 8, 2, 5
	peak := 0
	for attempt := 1; attempt <= attempts; attempt++ {
		probe, list := probeBackends(t, backends)
		calls, got := runFanOut(t, probe, search.NewMultiSearcher(list, search.WithMaxFanOut(limit)))

		if calls != backends {
			t.Fatalf("attempt %d: calls = %d, want %d — every backend must still be queried", attempt, calls, backends)
		}
		if got > limit {
			t.Fatalf("attempt %d: peak concurrency = %d, want <= %d", attempt, got, limit)
		}
		if got > peak {
			peak = got
		}
		if peak == limit {
			return
		}
	}
	t.Fatalf("peak concurrency = %d over %d attempts, want %d — the fan-out must run its backends concurrently, not serially", peak, attempts, limit)
}

// Boundary: with no option the default ceiling applies, below the backend count.
func TestMultiSearcher_DefaultFanOutIsBounded(t *testing.T) {
	t.Parallel()

	backends := search.DefaultMaxFanOut + 4
	probe, list := probeBackends(t, backends)
	calls, peak := runFanOut(t, probe, search.NewMultiSearcher(list))

	if calls != backends {
		t.Fatalf("calls = %d, want %d", calls, backends)
	}
	if peak > search.DefaultMaxFanOut {
		t.Fatalf("peak concurrency = %d, want <= DefaultMaxFanOut (%d)", peak, search.DefaultMaxFanOut)
	}
}

// Boundary: a limit of one serialises the fan-out instead of deadlocking.
func TestMultiSearcher_MaxFanOutOneIsSerial(t *testing.T) {
	t.Parallel()

	const backends = 4
	probe, list := probeBackends(t, backends)
	calls, peak := runFanOut(t, probe, search.NewMultiSearcher(list, search.WithMaxFanOut(1)))

	if calls != backends {
		t.Fatalf("calls = %d, want %d", calls, backends)
	}
	if peak != 1 {
		t.Fatalf("peak concurrency = %d, want exactly 1", peak)
	}
}

// Boundary: a limit above the backend count is clamped, not honoured literally.
func TestMultiSearcher_MaxFanOutAboveBackendCount(t *testing.T) {
	t.Parallel()

	const backends = 3
	probe, list := probeBackends(t, backends)
	calls, peak := runFanOut(t, probe, search.NewMultiSearcher(list, search.WithMaxFanOut(1000)))

	if calls != backends {
		t.Fatalf("calls = %d, want %d", calls, backends)
	}
	if peak > backends {
		t.Fatalf("peak concurrency = %d, want <= %d", peak, backends)
	}
}

// Negative: a non-positive bound is rejected — it must not produce a
// zero-capacity semaphore (which would deadlock the fan-out).
func TestMultiSearcher_MaxFanOutRejectsNonPositive(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, -1, -1000} {
		probe, list := probeBackends(t, search.DefaultMaxFanOut+2)
		m := search.NewMultiSearcher(list, search.WithMaxFanOut(limit))
		calls, peak := runFanOut(t, probe, m)

		if calls != search.DefaultMaxFanOut+2 {
			t.Fatalf("limit %d: calls = %d, want %d", limit, calls, search.DefaultMaxFanOut+2)
		}
		if peak > search.DefaultMaxFanOut {
			t.Fatalf("limit %d: peak = %d, want <= DefaultMaxFanOut (%d)", limit, peak, search.DefaultMaxFanOut)
		}
	}
}

// Negative: an empty backend list never reaches the semaphore at all.
func TestMultiSearcher_NoBackendsDoesNotFanOut(t *testing.T) {
	t.Parallel()

	m := search.NewMultiSearcher(nil, search.WithMaxFanOut(4))
	res, err := m.Search(context.Background(), "c", search.Query{Q: "q"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("hits = %d, want 0", len(res.Hits))
	}
}
