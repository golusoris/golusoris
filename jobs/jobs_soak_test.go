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

	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/testutil/load"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

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

func TestJobs_Soak(t *testing.T) {
	t.Parallel()
	w := &soakWorker{}
	rv := rivertest.Start(t, rivertest.Options{
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
		if _, err := rv.Client.Insert(opCtx, soakArgs{ID: id}, nil); err != nil {
			return fmt.Errorf("jobs soak insert: %w", err)
		}
		return nil
	}

	load.RunSoak(t, load.SoakOptions{
		Name:        "jobs",
		Pool:        rv.Pool,
		Pace:        10 * time.Millisecond,
		Concurrency: 2,
	}, op)
}
