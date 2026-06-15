package jobs_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type recordingObserver struct {
	mu       sync.Mutex
	inserted int
	finished chan string
}

func (o *recordingObserver) JobInserted(string, string) {
	o.mu.Lock()
	o.inserted++
	o.mu.Unlock()
}

func (o *recordingObserver) JobFinished(_, _, state string, _ time.Duration) {
	select {
	case o.finished <- state:
	default:
	}
}

// TestObserverSeesInsertAndCompletion is the #163 acceptance: an app can observe
// enqueue + completion without re-wrapping the river client.
func TestObserverSeesInsertAndCompletion(t *testing.T) {
	t.Parallel()
	obs := &recordingObserver{finished: make(chan string, 1)}
	rv := rivertest.Start(t, rivertest.Options{
		Observer: obs,
		Register: func(workers *jobs.Workers) { jobs.Register(workers, &greetWorker{}) },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := rv.Client.Insert(ctx, greetArgs{Name: "x"}, nil); err != nil {
		t.Fatalf("insert: %v", err)
	}

	select {
	case state := <-obs.finished:
		if state != "completed" {
			t.Errorf("JobFinished state = %q, want completed", state)
		}
	case <-ctx.Done():
		t.Fatal("JobFinished was never called")
	}

	obs.mu.Lock()
	got := obs.inserted
	obs.mu.Unlock()
	if got == 0 {
		t.Error("JobInserted was never called")
	}
}
