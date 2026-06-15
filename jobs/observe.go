package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Observer receives job lifecycle signals so apps can record metrics (enqueue
// counters, completion counters, a duration histogram) without re-wrapping the
// river client. Wire one via Options.Observer (code-supplied, e.g. fx.Decorate);
// nil means no observability. Implementations must be safe for concurrent use.
type Observer interface {
	// JobInserted is called once per successfully-enqueued job.
	JobInserted(queue, kind string)
	// JobFinished is called when a job reaches a terminal state; state is
	// "completed" or "failed", runDuration is the time spent executing.
	JobFinished(queue, kind, state string, runDuration time.Duration)
}

// insertObserver is a river insert middleware reporting each enqueued job.
type insertObserver struct {
	river.MiddlewareDefaults
	obs Observer
}

func (m *insertObserver) InsertMany(
	ctx context.Context,
	params []*rivertype.JobInsertParams,
	doInner func(ctx context.Context) ([]*rivertype.JobInsertResult, error),
) ([]*rivertype.JobInsertResult, error) {
	res, err := doInner(ctx)
	if err != nil {
		return res, err
	}
	for _, p := range params {
		queue := p.Queue
		if queue == "" {
			queue = river.QueueDefault
		}
		m.obs.JobInserted(queue, p.Kind)
	}
	return res, nil
}

// Observe subscribes to completed/failed events and forwards them to obs until
// the returned cancel is called. The client must already be started. The fx
// [Module] calls this automatically when Options.Observer is set; apps using
// [New] directly call it themselves after Start.
func Observe(c *Client, obs Observer) (cancel func()) {
	sub, cancel := c.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	go func() {
		for ev := range sub {
			state := "completed"
			if ev.Kind == river.EventKindJobFailed {
				state = "failed"
			}
			var dur time.Duration
			if ev.JobStats != nil {
				dur = ev.JobStats.RunDuration
			}
			if ev.Job != nil {
				obs.JobFinished(ev.Job.Queue, ev.Job.Kind, state, dur)
			}
		}
	}()
	return cancel
}
