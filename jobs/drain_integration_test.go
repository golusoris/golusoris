// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type blockArgs struct{}

func (blockArgs) Kind() string { return "drain-block" }

// blockWorker runs until its context is cancelled, like a stuck transcode.
type blockWorker struct {
	jobs.WorkerDefaults[blockArgs]
	started   chan struct{}
	cancelled atomic.Bool
}

func (w *blockWorker) Work(ctx context.Context, _ *jobs.Job[blockArgs]) error {
	close(w.started)
	<-ctx.Done()
	w.cancelled.Store(true)
	return ctx.Err()
}

func (*blockWorker) Timeout(*jobs.Job[blockArgs]) time.Duration { return time.Minute }

type flakyArgs struct{}

func (flakyArgs) Kind() string { return "retry-flaky" }

// flakyWorker fails its first three attempts.
type flakyWorker struct {
	jobs.WorkerDefaults[flakyArgs]
	attempts atomic.Int32
	done     chan struct{}
}

func (w *flakyWorker) Work(context.Context, *jobs.Job[flakyArgs]) error {
	if w.attempts.Add(1) <= 3 {
		return errors.New("transient")
	}
	close(w.done)
	return nil
}

func startClient(t *testing.T, opts jobs.Options, register func(*jobs.Workers) error) *jobs.Client {
	t.Helper()
	rv := rivertest.Start(t, rivertest.Options{})
	workers := jobs.NewWorkers()
	if err := register(workers); err != nil {
		t.Fatalf("register: %v", err)
	}
	opts.Enabled = true
	c, err := jobs.New(rv.Pool, opts, workers, discard())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return c
}

// TestDrain_cancelsStuckJobAfterSoftTimeout proves fx Stop cannot hang on a
// worker that ignores the soft phase: the hard phase cancels its context.
func TestDrain_cancelsStuckJobAfterSoftTimeout(t *testing.T) {
	t.Parallel()
	w := &blockWorker{started: make(chan struct{})}
	c := startClient(t, jobs.Options{}, func(ws *jobs.Workers) error { return jobs.Register(ws, w) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := c.Insert(ctx, blockArgs{}, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	select {
	case <-w.started:
	case <-ctx.Done():
		t.Fatal("blocking job never started")
	}
	if err := jobs.Drain(ctx, c, jobs.StopOptions{Soft: 100 * time.Millisecond, Hard: 5 * time.Second}, discard()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if !w.cancelled.Load() {
		t.Fatal("hard phase did not cancel the running job")
	}
}

// TestRetryPolicy_appliesToFailingJob: three failures with River's attempt^4
// default would take ~98s; a 20ms base finishes well inside the deadline.
func TestRetryPolicy_appliesToFailingJob(t *testing.T) {
	t.Parallel()
	w := &flakyWorker{done: make(chan struct{})}
	c := startClient(t, jobs.Options{
		Retry: jobs.RetryOptions{Base: 20 * time.Millisecond, Max: 200 * time.Millisecond},
		Job:   jobs.JobOptions{MaxAttempts: 5},
	}, func(ws *jobs.Workers) error { return jobs.Register(ws, w) })
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = jobs.Drain(stopCtx, c, jobs.StopOptions{}, discard())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := c.Insert(ctx, flakyArgs{}, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	select {
	case <-w.done:
	case <-ctx.Done():
		t.Fatalf("job not completed after %d attempts; retry policy not applied", w.attempts.Load())
	}
}
