// Package river boots a [jobs.Client] backed by a real Postgres
// container (via testutil/pg) and the river migrations applied. Used by
// tests that need to exercise actual worker execution.
//
// Usage:
//
//	func TestMyWorker(t *testing.T) {
//	    rv := rivertest.Start(t, rivertest.Options{
//	        Register: func(w *jobs.Workers) {
//	            jobs.Register(w, &MyWorker{})
//	        },
//	    })
//	    _, err := rv.Client.Insert(ctx, MyArgs{ID: "x"}, nil)
//	    ...
//	}
//
// Each call spins a fresh Postgres + river setup — tests are isolated.
// Docker is required (testutil/pg contract).
package river

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/golusoris/golusoris/jobs"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// Options tunes the harness.
type Options struct {
	// Register is invoked after NewWorkers() so the test can register
	// worker implementations before the client starts. Optional — an
	// insert-only client is built when nil.
	Register func(w *jobs.Workers)
	// JobTimeout caps each job invocation (default 5s for fast tests).
	JobTimeout time.Duration
	// StartTimeout caps the client Start call (default 10s).
	StartTimeout time.Duration
}

// Harness is returned by [Start]. Fields are set; don't mutate.
type Harness struct {
	Pool    *pgxpool.Pool
	Workers *jobs.Workers
	Client  *jobs.Client
}

// Start boots the harness. Tears everything down via t.Cleanup.
func Start(t *testing.T, opts Options) *Harness {
	t.Helper()
	if opts.JobTimeout == 0 {
		opts.JobTimeout = 5 * time.Second
	}
	if opts.StartTimeout == 0 {
		opts.StartTimeout = 10 * time.Second
	}

	pool := pgtest.Start(t)

	// Migration ctx is short-lived, scoped to this function only.
	migCtx, migCancel := context.WithTimeout(context.Background(), opts.StartTimeout)
	defer migCancel()

	// Apply river's own migrations.
	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatalf("testutil/river: build migrator: %v", err)
	}
	if _, migErr := migrator.Migrate(migCtx, rivermigrate.DirectionUp, nil); migErr != nil {
		t.Fatalf("testutil/river: migrate: %v", migErr)
	}

	workers := jobs.NewWorkers()
	if opts.Register != nil {
		opts.Register(workers)
	}

	logger := slog.New(slog.DiscardHandler)
	client, err := jobs.New(pool, jobs.Options{
		Enabled: true,
		Queue:   jobs.QueueOptions{Default: jobs.QueueDefault{Max: 2}},
		Job:     jobs.JobOptions{Timeout: opts.JobTimeout, MaxAttempts: 3},
	}, workers, logger)
	if err != nil {
		t.Fatalf("testutil/river: new client: %v", err)
	}

	// Only Start when workers are registered — otherwise it's insert-only.
	// Start ctx must outlive this function: river uses it as the parent
	// for its long-running pollers. Tie cancellation to t.Cleanup.
	if opts.Register != nil {
		startCtx, startCancel := context.WithCancel(context.Background())
		if err := client.Start(startCtx); err != nil {
			startCancel()
			t.Fatalf("testutil/river: start: %v", err)
		}
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			_ = client.Stop(stopCtx)
			startCancel()
		})
	}

	return &Harness{Pool: pool, Workers: workers, Client: client}
}

// WaitForJob polls the river jobs table until a job matching kind reaches
// the terminal state (completed or discarded), or the deadline fires.
func (h *Harness) WaitForJob(ctx context.Context, kind string) (*river.JobListResult, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	for time.Now().Before(deadline) {
		params := river.NewJobListParams().Kinds(kind).First(1)
		res, err := h.Client.JobList(ctx, params)
		if err != nil {
			return nil, err //nolint:wrapcheck // passthrough to caller
		}
		if len(res.Jobs) > 0 {
			j := res.Jobs[0]
			if j.State == "completed" || j.State == "discarded" {
				return res, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err() //nolint:wrapcheck // passthrough to caller
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, context.DeadlineExceeded
}
