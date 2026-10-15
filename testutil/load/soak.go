// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package load

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
)

const (
	// DefaultSoakDuration provides sufficient duration for steady-state heap and worker leak detection.
	DefaultSoakDuration = 2 * time.Minute // WHY: provides sufficient window for steady-state leak detection.
	// MaxSoakDuration caps total run time to prevent runaway execution in CI.
	MaxSoakDuration = 30 * time.Minute // WHY: HISS-02 scalar upper bound preventing runaway soak execution in CI.
	// MinSoakDuration ensures minimum duration for warmup and observation windows.
	MinSoakDuration = 5 * time.Second // WHY: ensures minimum duration for warmup and observation windows.
	// WarmupRatio discards initial startup transients such as connection establishment and JIT allocation.
	WarmupRatio = 0.20 // WHY: discards initial startup transients before measuring steady-state metrics.
	// DefaultSampleInterval yields steady sampling cadence without adding runtime metric collection overhead.
	DefaultSampleInterval = 1 * time.Second // WHY: yields steady sampling cadence without adding metric collection overhead.
	// DefaultPace prevents unpaced busy loops from exhausting MaxLoopIterations.
	DefaultPace = 10 * time.Millisecond // WHY: prevents unpaced busy loops from exhausting MaxLoopIterations.
	// DefaultMaxGoroutineSlack accommodates Go runtime GC, timer, and testcontainer pollers without masking per-op leaks.
	DefaultMaxGoroutineSlack = 15 // WHY: accommodates runtime GC, timers, and container pollers without masking leaks.
	// DefaultMaxHeapSlopeBytesPerSec caps steady-state retained heap accumulation at 50 KiB/s.
	DefaultMaxHeapSlopeBytesPerSec = 50 * 1024 // WHY: caps steady-state retained heap accumulation at 50 KiB/s to catch memory leaks.
	// DefaultMinThroughputRatio fails if throughput decays by more than 30% due to lock contention or starvation.
	DefaultMinThroughputRatio = 0.70 // WHY: fails if throughput decays by more than 30% due to contention or pool starvation.
	// MaxPoolAcquiredSlack bounds idle connections checked out after workload stops (< default MaxConns of 4).
	MaxPoolAcquiredSlack = 1 // WHY: bounds idle pool connections checked out after workload stops (< default MaxConns of 4).
	// MaxLoopIterations bounds workload execution loops to satisfy HISS-02.
	MaxLoopIterations = 1_000_000 // WHY: HISS-02 scalar bound for workload execution loop.
	// MaxSampleCount bounds 1s sample collections over maximum duration.
	MaxSampleCount = 3_600 // WHY: HISS-02 scalar bound for 1s sample collections over maximum duration.
	// MaxConcurrency bounds worker fan-out to satisfy HISS-06.
	MaxConcurrency = 16 // WHY: HISS-06 upper bound on soak worker fan-out.
)

// Sample records a single point-in-time measurement during a soak run.
type Sample struct {
	Timestamp     time.Time
	Goroutines    int
	HeapBytes     uint64
	AcquiredConns int32
	OpsCompleted  int64
}

// SoakOptions configures a soak run.
type SoakOptions struct {
	Name                    string
	Duration                time.Duration
	SampleInterval          time.Duration
	Concurrency             int
	Pace                    time.Duration
	Pool                    *pgxpool.Pool
	MaxGoroutineSlack       int
	MaxPoolAcquiredSlack    int
	MaxHeapSlopeBytesPerSec float64
	MinThroughputRatio      float64
	MaxIterations           int
}

// SoakResult holds the aggregated outcome of a soak run.
type SoakResult struct {
	Area            string
	Duration        time.Duration
	TotalOps        int64
	WarmupGoroutine int
	EndGoroutine    int
	GoroutineDelta  int
	HeapSlope       float64
	FirstWindowOps  float64
	LastWindowOps   float64
	Samples         []Sample
}

// soakTB is the testing.T subset RunSoak reports through; tests substitute a spy.
type soakTB interface {
	Helper()
	Error(args ...any)
}

// RunSoak executes a steady workload for the resolved duration, sampling runtime
// metrics and asserting goroutine, heap slope, error, and throughput bounds.
func RunSoak(t *testing.T, opts SoakOptions, op func(context.Context) error) SoakResult {
	t.Helper()
	return runSoakTB(t, opts, op)
}

func runSoakTB(tb soakTB, opts SoakOptions, op func(context.Context) error) SoakResult {
	tb.Helper()
	duration := resolveSoakDuration(opts.Duration)
	warmup := time.Duration(float64(duration) * WarmupRatio)

	opts = normalizeOptions(opts)
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var (
		totalOps atomic.Int64
		firstErr atomic.Pointer[error]
		samples  = make([]Sample, 0, 16)
		sampMu   sync.Mutex
	)

	stopSampler := startSampler(ctx, opts, &totalOps, &samples, &sampMu)
	defer stopSampler()

	recordOp := func() { totalOps.Add(1) }
	recordErr := func(err error) {
		firstErr.CompareAndSwap(nil, &err)
		cancel()
	}
	runWorkload(ctx, opts.Concurrency, opts.Pace, opts.MaxIterations, op, recordOp, recordErr)
	stopSampler()

	if ctx.Err() == nil && firstErr.Load() == nil {
		err := fmt.Errorf("load: soak [%s]: workers stopped before duration (%v) expired", opts.Name, duration)
		firstErr.CompareAndSwap(nil, &err)
	}

	clk := clockwork.NewRealClock()
	sEnd := sampleMetrics(clk.Now(), opts.Pool, totalOps.Load())
	samples = append(samples, sEnd)

	res := buildSoakResult(opts.Name, duration, warmup, totalOps.Load(), samples)
	var errVal error
	if ep := firstErr.Load(); ep != nil {
		errVal = *ep
	}
	verifySoakResults(tb, opts, res, errVal)
	if err := emitStepSummary(res); err != nil {
		tb.Error(err)
	}
	return res
}

func startSampler(
	ctx context.Context,
	opts SoakOptions,
	totalOps *atomic.Int64,
	samples *[]Sample,
	mu *sync.Mutex,
) func() {
	clk := clockwork.NewRealClock()

	s0 := sampleMetrics(clk.Now(), opts.Pool, totalOps.Load())
	mu.Lock()
	*samples = append(*samples, s0)
	mu.Unlock()

	ticker := clk.NewTicker(opts.SampleInterval)
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Go(func() {
		defer ticker.Stop()
		for i := 0; i < MaxSampleCount && ctx.Err() == nil; i++ {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case now := <-ticker.Chan():
				s := sampleMetrics(now, opts.Pool, totalOps.Load())
				mu.Lock()
				if len(*samples) < MaxSampleCount {
					*samples = append(*samples, s)
				}
				mu.Unlock()
			}
		}
	})

	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}

func runWorkload(
	ctx context.Context,
	concurrency int,
	pace time.Duration,
	maxIterations int,
	op func(context.Context) error,
	recordOp func(),
	recordErr func(error),
) {
	var wg sync.WaitGroup
	clk := clockwork.NewRealClock()
	for i := 0; i < concurrency && i < MaxConcurrency; i++ {
		wg.Go(func() {
			runWorker(ctx, clk, pace, maxIterations, op, recordOp, recordErr)
		})
	}
	wg.Wait()
}

func runWorker(
	ctx context.Context,
	clk clockwork.Clock,
	pace time.Duration,
	maxIterations int,
	op func(context.Context) error,
	recordOp func(),
	recordErr func(error),
) {
	for iter := 0; iter < maxIterations && ctx.Err() == nil; iter++ {
		if err := op(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			recordErr(err)
			return
		}
		recordOp()
		if pace > 0 && !waitForPace(ctx, clk, pace) {
			return
		}
	}
}

func waitForPace(ctx context.Context, clk clockwork.Clock, pace time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-clk.After(pace):
		return true
	}
}

func sampleMetrics(now time.Time, pool *pgxpool.Pool, ops int64) Sample {
	runtime.GC()
	return Sample{
		Timestamp:     now,
		Goroutines:    runtime.NumGoroutine(),
		HeapBytes:     readHeapInUse(),
		AcquiredConns: readPoolAcquired(pool),
		OpsCompleted:  ops,
	}
}

func readHeapInUse() uint64 {
	sample := make([]metrics.Sample, 1)
	sample[0].Name = "/memory/classes/heap/objects:bytes"
	metrics.Read(sample)
	if sample[0].Value.Kind() == metrics.KindUint64 {
		return sample[0].Value.Uint64()
	}
	return 0
}

func readPoolAcquired(pool *pgxpool.Pool) int32 {
	if pool == nil {
		return 0
	}
	return pool.Stat().AcquiredConns()
}

func buildSoakResult(
	name string,
	duration, warmup time.Duration,
	totalOps int64,
	samples []Sample,
) SoakResult {
	warmupIdx := findWarmupIndex(samples, warmup)
	slope := calculateHeapSlope(samples, warmupIdx)
	firstOps, lastOps := calculateThroughputWindows(samples, warmupIdx)

	warmupG := 0
	endG := 0
	if len(samples) > 0 {
		warmupG = samples[warmupIdx].Goroutines
		endG = samples[len(samples)-1].Goroutines
	}

	return SoakResult{
		Area:            name,
		Duration:        duration,
		TotalOps:        totalOps,
		WarmupGoroutine: warmupG,
		EndGoroutine:    endG,
		GoroutineDelta:  endG - warmupG,
		HeapSlope:       slope,
		FirstWindowOps:  firstOps,
		LastWindowOps:   lastOps,
		Samples:         samples,
	}
}

func findWarmupIndex(samples []Sample, warmup time.Duration) int {
	if len(samples) == 0 {
		return 0
	}
	start := samples[0].Timestamp
	for i := 0; i < len(samples) && i < MaxSampleCount; i++ {
		if samples[i].Timestamp.Sub(start) >= warmup {
			return i
		}
	}
	return len(samples) / 4
}

func calculateHeapSlope(samples []Sample, warmupIdx int) float64 {
	if len(samples) <= warmupIdx+1 {
		return 0
	}
	pts := samples[warmupIdx:]
	if len(pts) > MaxSampleCount {
		pts = pts[:MaxSampleCount]
	}
	n := float64(len(pts))
	t0 := pts[0].Timestamp

	var sumT, sumY, sumTT, sumTY float64
	for i := range pts {
		t := pts[i].Timestamp.Sub(t0).Seconds()
		y := float64(pts[i].HeapBytes)
		sumT += t
		sumY += y
		sumTT += t * t
		sumTY += t * y
	}

	denom := n*sumTT - sumT*sumT
	if denom == 0 {
		return 0
	}
	return (n*sumTY - sumT*sumY) / denom
}

func calculateThroughputWindows(samples []Sample, warmupIdx int) (float64, float64) {
	if len(samples) <= warmupIdx+1 {
		return 0, 0
	}
	post := samples[warmupIdx:]
	n := len(post)
	wSize := max(n/3, 1)

	firstDeltaOps := post[wSize].OpsCompleted - post[0].OpsCompleted
	firstDeltaT := post[wSize].Timestamp.Sub(post[0].Timestamp).Seconds()
	firstRate := 0.0
	if firstDeltaT > 0 {
		firstRate = float64(firstDeltaOps) / firstDeltaT
	}

	lastDeltaOps := post[n-1].OpsCompleted - post[n-1-wSize].OpsCompleted
	lastDeltaT := post[n-1].Timestamp.Sub(post[n-1-wSize].Timestamp).Seconds()
	lastRate := 0.0
	if lastDeltaT > 0 {
		lastRate = float64(lastDeltaOps) / lastDeltaT
	}

	return firstRate, lastRate
}

func verifySoakResults(tb soakTB, opts SoakOptions, res SoakResult, firstErr error) {
	tb.Helper()
	for _, v := range violations(opts, res, firstErr) {
		tb.Error(v)
	}
}

// violations lists every bound the run broke; empty means the run passed.
func violations(opts SoakOptions, res SoakResult, firstErr error) []string {
	var out []string
	if firstErr != nil {
		out = append(out, fmt.Sprintf("soak [%s]: operation error: %v", opts.Name, firstErr))
	}
	warmup := time.Duration(float64(res.Duration) * WarmupRatio)
	warmupIdx := findWarmupIndex(res.Samples, warmup)
	postSamples := len(res.Samples) - warmupIdx
	if len(res.Samples) == 0 {
		postSamples = 0
	}
	if postSamples < 3 {
		out = append(out, fmt.Sprintf("soak [%s]: insufficient post-warmup samples: got %d, want at least 3", opts.Name, postSamples))
	}
	if res.GoroutineDelta > opts.MaxGoroutineSlack {
		out = append(out, fmt.Sprintf("soak [%s]: goroutine count grew by %d after warmup (%d -> %d), exceeds slack %d",
			opts.Name, res.GoroutineDelta, res.WarmupGoroutine, res.EndGoroutine, opts.MaxGoroutineSlack))
	}
	if res.HeapSlope > opts.MaxHeapSlopeBytesPerSec {
		out = append(out, fmt.Sprintf("soak [%s]: heap slope %.2f B/s exceeds bound %.2f B/s",
			opts.Name, res.HeapSlope, opts.MaxHeapSlopeBytesPerSec))
	}
	if res.FirstWindowOps > 0 && res.LastWindowOps < opts.MinThroughputRatio*res.FirstWindowOps {
		out = append(out, fmt.Sprintf("soak [%s]: throughput decayed from %.2f ops/s to %.2f ops/s (below %.0f%%)",
			opts.Name, res.FirstWindowOps, res.LastWindowOps, opts.MinThroughputRatio*100))
	}
	poolSlack := opts.MaxPoolAcquiredSlack
	if poolSlack <= 0 {
		poolSlack = MaxPoolAcquiredSlack
	}
	if v := checkPoolViolation(opts.Name, poolSlack, res.Samples); v != "" {
		out = append(out, v)
	}
	return out
}

func checkPoolViolation(name string, slack int, samples []Sample) string {
	if len(samples) == 0 {
		return ""
	}
	if acquired := samples[len(samples)-1].AcquiredConns; acquired > int32(slack) {
		return fmt.Sprintf("soak [%s]: pool acquired connections %d exceeds slack %d", name, acquired, slack)
	}
	return ""
}

func resolveSoakDuration(override time.Duration) time.Duration {
	if override > 0 {
		return clampDuration(override)
	}
	envVal := os.Getenv("SOAK_DURATION")
	if envVal != "" {
		if d, err := time.ParseDuration(envVal); err == nil && d > 0 {
			return clampDuration(d)
		}
	}
	return DefaultSoakDuration
}

func clampDuration(d time.Duration) time.Duration {
	if d > MaxSoakDuration {
		return MaxSoakDuration
	}
	if d < MinSoakDuration {
		return MinSoakDuration
	}
	return d
}

func normalizeOptions(opts SoakOptions) SoakOptions {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Concurrency > MaxConcurrency {
		opts.Concurrency = MaxConcurrency
	}
	if opts.SampleInterval <= 0 {
		opts.SampleInterval = DefaultSampleInterval
	}
	if opts.Pace <= 0 {
		opts.Pace = DefaultPace
	}
	if opts.MaxGoroutineSlack <= 0 {
		opts.MaxGoroutineSlack = DefaultMaxGoroutineSlack
	}
	if opts.MaxPoolAcquiredSlack <= 0 {
		opts.MaxPoolAcquiredSlack = MaxPoolAcquiredSlack
	}
	if opts.MaxHeapSlopeBytesPerSec <= 0 {
		opts.MaxHeapSlopeBytesPerSec = DefaultMaxHeapSlopeBytesPerSec
	}
	if opts.MinThroughputRatio <= 0 {
		opts.MinThroughputRatio = DefaultMinThroughputRatio
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = MaxLoopIterations
	}
	return opts
}

// emitStepSummary appends the run's row to the GitHub job summary when one is set.
func emitStepSummary(res SoakResult) (err error) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // WHY: path is the runner-provided summary file.
	if err != nil {
		return fmt.Errorf("load: open step summary: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("load: close step summary: %w", cerr)
		}
	}()
	if _, err = fmt.Fprintf(f, "| %s | %s | %d &rarr; %d (%+d) | %.2f B/s | %.2f &rarr; %.2f |\n",
		res.Area, res.Duration.Round(time.Second),
		res.WarmupGoroutine, res.EndGoroutine, res.GoroutineDelta,
		res.HeapSlope, res.FirstWindowOps, res.LastWindowOps,
	); err != nil {
		return fmt.Errorf("load: write step summary: %w", err)
	}
	return nil
}
