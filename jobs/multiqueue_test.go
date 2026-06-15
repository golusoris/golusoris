package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type probeArgs struct{}

func (probeArgs) Kind() string { return "probe" }

type probeWorker struct {
	river.WorkerDefaults[probeArgs]
	ran chan struct{}
}

func (w *probeWorker) Work(context.Context, *river.Job[probeArgs]) error {
	select {
	case w.ran <- struct{}{}:
	default:
	}
	return nil
}

// TestMultiQueueJobsAreWorked is the #162 regression: a job inserted into a
// non-default ("critical") queue must actually be worked. Before multi-queue
// support, only "default" was wired, so such jobs were silently lost.
func TestMultiQueueJobsAreWorked(t *testing.T) {
	t.Parallel()
	w := &probeWorker{ran: make(chan struct{}, 1)}
	rv := rivertest.Start(t, rivertest.Options{
		Queues:   map[string]jobs.QueueConfig{"critical": {Max: 2}},
		Register: func(workers *jobs.Workers) { jobs.Register(workers, w) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := rv.Client.Insert(ctx, probeArgs{}, &river.InsertOpts{Queue: "critical"}); err != nil {
		t.Fatalf("insert into 'critical': %v", err)
	}
	select {
	case <-w.ran:
	case <-ctx.Done():
		t.Fatal("job in non-default 'critical' queue was never worked (multi-queue regression)")
	}
}
