package fleet_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/fleet"
	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

// chanSink unblocks a test once a prediction is stored.
type chanSink struct {
	mu  sync.Mutex
	got chan tiny.Prediction
}

func (c *chanSink) Store(_ context.Context, _ tiny.Ref, p tiny.Prediction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case c.got <- p:
	default:
	}
	return nil
}

// TestFleet_endToEnd boots a real river/Postgres harness, wires the
// node-side Worker on the "tiny-gpu" capability queue, submits a
// prediction via the controller Fleet, and asserts the worker matched
// the capability, ran the predictor, and stored the result. Skips
// without Docker (testutil/pg contract).
func TestFleet_endToEnd(t *testing.T) {
	t.Parallel()

	reg := tiny.NewMemoryRegistry()
	model := &tiny.Model{Name: "intent", Modality: tiny.ModalityText, TaskKind: tiny.TaskGenerate}
	require.NoError(t, reg.SaveModel(context.Background(), model))

	sink := &chanSink{got: make(chan tiny.Prediction, 1)}
	pred := &stubPredictor{out: tiny.Prediction{Text: "support"}}

	worker, err := fleet.NewWorker(reg, fleet.SingletonFactory(pred), sink,
		[]fleet.Capability{"gpu"}, 5*time.Second, 0, nil)
	require.NoError(t, err)

	rv := rivertest.Start(t, rivertest.Options{
		// Register the capability queue up front so the producer starts
		// with the client (harness builds river queues from this map).
		Queues: map[string]jobs.QueueConfig{"tiny-gpu": {Max: 2}},
		Register: func(w *jobs.Workers) {
			jobs.Register(w, worker)
		},
	})

	f, err := fleet.NewFleet(reg, rv.Client, "tiny")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id, err := f.Submit(ctx, fleet.Request{
		Model:      tiny.Ref{Name: "intent"},
		Capability: "gpu",
		Input:      "my card was charged twice",
	})
	require.NoError(t, err)
	require.Positive(t, id)

	select {
	case got := <-sink.got:
		require.Equal(t, "support", got.Text)
	case <-ctx.Done():
		t.Fatal("prediction never stored — capability queue was not worked")
	}
}

// TestFleet_dynamicQueueAdd proves the recipe's runtime queue
// subscription: a queue absent at client construction is added via
// Client.Queues().Add and then worked. This mirrors fleet.Module's
// addQueues hook.
func TestFleet_dynamicQueueAdd(t *testing.T) {
	t.Parallel()

	reg := tiny.NewMemoryRegistry()
	require.NoError(t, reg.SaveModel(context.Background(),
		&tiny.Model{Name: "m", Modality: tiny.ModalityText, TaskKind: tiny.TaskGenerate}))

	sink := &chanSink{got: make(chan tiny.Prediction, 1)}
	worker, err := fleet.NewWorker(reg, fleet.SingletonFactory(&stubPredictor{out: tiny.Prediction{Text: "ok"}}),
		sink, []fleet.Capability{"cpu"}, 5*time.Second, 0, nil)
	require.NoError(t, err)

	// Start with NO capability queue; add it dynamically after start.
	rv := rivertest.Start(t, rivertest.Options{
		Register: func(w *jobs.Workers) { jobs.Register(w, worker) },
	})
	require.NoError(t, rv.Client.Queues().Add("tiny-cpu", river.QueueConfig{MaxWorkers: 2}))

	f, err := fleet.NewFleet(reg, rv.Client, "tiny")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = f.Submit(ctx, fleet.Request{Model: tiny.Ref{Name: "m"}, Capability: "cpu", Input: "x"})
	require.NoError(t, err)

	select {
	case <-sink.got:
	case <-ctx.Done():
		t.Fatal("dynamically-added queue was not worked")
	}
}
