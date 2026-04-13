package jobs_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

// greetArgs + greetWorker exercise the full round trip: register,
// insert, execute.
type greetArgs struct {
	Name string `json:"name"`
}

func (greetArgs) Kind() string { return "greet" }

type greetWorker struct {
	river.WorkerDefaults[greetArgs]
	called atomic.Int32
}

func (w *greetWorker) Work(_ context.Context, _ *river.Job[greetArgs]) error {
	w.called.Add(1)
	return nil
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := jobs.DefaultOptions()
	if !o.Enabled {
		t.Error("Enabled should default true")
	}
	if o.Queue.Default.Max != 10 {
		t.Errorf("Queue.Default.Max = %d", o.Queue.Default.Max)
	}
	if o.Job.Timeout != 30*time.Second {
		t.Errorf("Job.Timeout = %v", o.Job.Timeout)
	}
}

// TestEndToEnd boots a real river harness, enqueues a job, and verifies
// the worker ran.
func TestEndToEnd(t *testing.T) {
	t.Parallel()
	w := &greetWorker{}
	rv := rivertest.Start(t, rivertest.Options{
		Register: func(workers *jobs.Workers) {
			jobs.Register(workers, w)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := rv.Client.Insert(ctx, greetArgs{Name: "world"}, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w.called.Load() >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("worker not called; called=%d", w.called.Load())
}
