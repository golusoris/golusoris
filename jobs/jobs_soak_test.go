// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build soak

package jobs_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/testutil/load"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

const soakQueue = "soak"

type soakArgs struct {
	ID int64 `json:"id"`
}

func (soakArgs) Kind() string { return "soak_job" }

type soakWorker struct {
	river.WorkerDefaults[soakArgs]
	processed atomic.Int64
}

func (w *soakWorker) Work(_ context.Context, _ *river.Job[soakArgs]) error {
	w.processed.Add(1)
	return nil
}

//nolint:paralleltest // WHY: soak tests sample process-wide metrics; parallel tests skew goroutines and heap.
func TestJobs_Soak(t *testing.T) {
	w := &soakWorker{}
	rv := rivertest.Start(t, rivertest.Options{
		// WHY: the harness default queue runs 2 workers; with a 100ms fetch cooldown that drains ~20 jobs/s, below the ~40/s insert pace.
		Queues: map[string]jobs.QueueConfig{soakQueue: {Max: 2}},
		Register: func(workers *jobs.Workers) {
			if err := jobs.Register(workers, w); err != nil {
				t.Fatalf("Register: %v", err)
			}
		},
	})

	var counter atomic.Int64
	op := func(ctx context.Context) error {
		id := counter.Add(1)
		opCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if _, err := rv.Client.Insert(opCtx, soakArgs{ID: id}, &river.InsertOpts{Queue: soakQueue}); err != nil {
			return fmt.Errorf("jobs soak insert: %w", err)
		}
		return nil
	}

	res := load.RunSoak(t, load.SoakOptions{
		Name:        "jobs",
		Pool:        rv.Pool,
		Pace:        50 * time.Millisecond,
		Concurrency: 2,
	}, op)

	inserted := counter.Load()
	if inserted == 0 || res.TotalOps == 0 {
		t.Fatal("jobs soak: no jobs were inserted")
	}

	// Drain: wait for workers to process remaining jobs before asserting.
	clk := clockwork.NewRealClock()
	deadline := clk.Now().Add(10 * time.Second)
	for clk.Now().Before(deadline) && w.processed.Load() < inserted {
		clk.Sleep(50 * time.Millisecond)
	}

	processed := w.processed.Load()
	if processed == 0 {
		t.Fatal("jobs soak: no jobs were processed")
	}
	if float64(processed) < float64(inserted)*0.90 {
		t.Fatalf("jobs soak: processed %d jobs, want close to inserted %d", processed, inserted)
	}
}
