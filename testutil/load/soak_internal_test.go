// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package load

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const samples = 60

// series builds samples one-second samples with the given goroutine, heap and ops curves.
func series(goroutines func(int) int, heap func(int) uint64, ops func(int) int64) []Sample {
	start := time.Unix(0, 0)
	out := make([]Sample, samples)
	for i := range samples {
		out[i] = Sample{
			Timestamp:    start.Add(time.Duration(i) * time.Second),
			Goroutines:   goroutines(i),
			HeapBytes:    heap(i),
			OpsCompleted: ops(i),
		}
	}
	return out
}

func flatGoroutines(int) int { return 20 }
func flatHeap(int) uint64    { return 1 << 20 }
func steadyOps(i int) int64  { return int64(i) * 100 }
func soakOpts() SoakOptions  { return normalizeOptions(SoakOptions{Name: "plant"}) }
func result(s []Sample) SoakResult {
	return buildSoakResult("plant", time.Duration(len(s))*time.Second, time.Duration(len(s))*time.Second/5, 0, s)
}

func wantOne(t *testing.T, got []string, substr string) {
	t.Helper()
	if len(got) != 1 || !strings.Contains(got[0], substr) {
		t.Fatalf("violations = %q, want one containing %q", got, substr)
	}
}

func TestViolationsSteadyRunPasses(t *testing.T) {
	t.Parallel()
	res := result(series(flatGoroutines, flatHeap, steadyOps))
	if got := violations(soakOpts(), res, nil); len(got) != 0 {
		t.Fatalf("steady run violations = %q", got)
	}
}

func TestViolationsGoroutineLeak(t *testing.T) {
	t.Parallel()
	leak := func(i int) int { return 20 + i } // one leaked goroutine per second
	wantOne(t, violations(soakOpts(), result(series(leak, flatHeap, steadyOps)), nil), "goroutine count grew")
}

func TestViolationsGoroutineSlackBoundary(t *testing.T) {
	t.Parallel()
	opts := soakOpts()
	atSlack := func(i int) int {
		if i == samples-1 {
			return 20 + opts.MaxGoroutineSlack
		}
		return 20
	}
	if got := violations(opts, result(series(atSlack, flatHeap, steadyOps)), nil); len(got) != 0 {
		t.Fatalf("growth equal to the slack must pass: %q", got)
	}
}

func TestViolationsHeapGrowth(t *testing.T) {
	t.Parallel()
	grow := func(i int) uint64 { return 1<<20 + uint64(i)*100*1024 } // 100 KiB/s retained
	wantOne(t, violations(soakOpts(), result(series(flatGoroutines, grow, steadyOps)), nil), "heap slope")
}

func TestViolationsThroughputDecay(t *testing.T) {
	t.Parallel()
	decay := func(i int) int64 {
		if i < 30 {
			return int64(i) * 100
		}
		return 3000 + int64(i-30)*10
	}
	wantOne(t, violations(soakOpts(), result(series(flatGoroutines, flatHeap, decay)), nil), "throughput decayed")
}

func TestViolationsOperationError(t *testing.T) {
	t.Parallel()
	res := result(series(flatGoroutines, flatHeap, steadyOps))
	wantOne(t, violations(soakOpts(), res, errors.New("planted")), "operation error: planted")
}

func TestViolationsEmptySamples(t *testing.T) {
	t.Parallel()
	if got := violations(soakOpts(), result(nil), nil); len(got) != 0 {
		t.Fatalf("empty run violations = %q", got)
	}
}

//nolint:paralleltest // WHY: counts process goroutines; parallel tests would skew the count.
func TestSampleMetricsSeesLeakedGoroutines(t *testing.T) {
	before := sampleMetrics(time.Unix(0, 0), nil, 0).Goroutines
	stop := make(chan struct{})
	const leaked = 50
	for range leaked {
		go func() { <-stop }()
	}
	after := sampleMetrics(time.Unix(1, 0), nil, 0).Goroutines
	close(stop)
	if after-before < leaked {
		t.Fatalf("goroutines %d -> %d, want at least %d more", before, after, leaked)
	}
}

func TestResolveSoakDurationClamps(t *testing.T) {
	t.Parallel()
	if got := resolveSoakDuration(time.Hour); got != MaxSoakDuration {
		t.Fatalf("1h -> %v, want %v", got, MaxSoakDuration)
	}
	if got := resolveSoakDuration(time.Millisecond); got != MinSoakDuration {
		t.Fatalf("1ms -> %v, want %v", got, MinSoakDuration)
	}
}
