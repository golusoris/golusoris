// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cron_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/jobs/cron"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type tickArgs struct{}

func (tickArgs) Kind() string { return "cron-tick-job" }

type tickWorker struct {
	river.WorkerDefaults[tickArgs]
	received chan struct{}
}

func (w *tickWorker) Work(_ context.Context, _ *river.Job[tickArgs]) error {
	select {
	case w.received <- struct{}{}:
	default:
	}
	return nil
}

func TestRegister_RealRiver(t *testing.T) {
	t.Parallel()

	worker := &tickWorker{received: make(chan struct{}, 1)}
	rv := rivertest.Start(t, rivertest.Options{
		Register: func(w *jobs.Workers) {
			if err := jobs.Register(w, worker); err != nil {
				t.Fatalf("jobs.Register: %v", err)
			}
		},
	})

	var constructorCalls atomic.Int32
	badErr := cron.Register(rv.Client, "invalid * * * * *", func() tickArgs {
		constructorCalls.Add(1)
		return tickArgs{}
	})
	if badErr == nil {
		t.Fatal("Register with invalid expr succeeded, want error")
	}
	if calls := constructorCalls.Load(); calls != 0 {
		t.Fatalf("constructor called %d times on invalid expr, want 0", calls)
	}

	if err := cron.Register(rv.Client, "@every 1s", func() tickArgs {
		return tickArgs{}
	}); err != nil {
		t.Fatalf("Register(@every 1s): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	select {
	case <-worker.received:
	case <-ctx.Done():
		t.Fatal("worker did not receive periodic job within timeout")
	}
}
