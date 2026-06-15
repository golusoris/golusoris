package fleet_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/fleet"
)

// fakeInserter records the last insert and returns a canned job ID.
type fakeInserter struct {
	mu      sync.Mutex
	gotArgs river.JobArgs
	gotOpts *river.InsertOpts
	id      int64
	err     error
}

func (f *fakeInserter) Insert(_ context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotArgs = args
	f.gotOpts = opts
	if f.err != nil {
		return nil, f.err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: f.id}}, nil
}

// seedRegistry returns a MemoryRegistry with one generate model saved.
func seedRegistry(t *testing.T) (*tiny.MemoryRegistry, tiny.Ref) {
	t.Helper()
	reg := tiny.NewMemoryRegistry()
	m := &tiny.Model{
		Name:     "intent",
		Modality: tiny.ModalityText,
		TaskKind: tiny.TaskGenerate,
	}
	require.NoError(t, reg.SaveModel(context.Background(), m))
	return reg, tiny.Ref{Name: "intent"}
}

func TestSubmit_routesToCapabilityQueue(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	ins := &fakeInserter{id: 42}
	f, err := fleet.NewFleet(reg, ins, "tiny")
	require.NoError(t, err)

	id, err := f.Submit(context.Background(), fleet.Request{
		Model:      ref,
		Capability: "gpu",
		Input:      "classify this",
		Priority:   2,
		Tags:       []string{"t1"},
	})
	require.NoError(t, err)
	require.Equal(t, int64(42), id)
	require.Equal(t, "tiny-gpu", ins.gotOpts.Queue)
	require.Equal(t, 2, ins.gotOpts.Priority)
	require.Equal(t, []string{"t1"}, ins.gotOpts.Tags)

	args, ok := ins.gotArgs.(fleet.PredictArgs)
	require.True(t, ok)
	require.Equal(t, fleet.Capability("gpu"), args.Capability)
	require.Equal(t, "classify this", args.Input)
	require.Equal(t, "intent", args.Ref.Name)
}

func TestSubmit_defaultPrefixWhenEmpty(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	ins := &fakeInserter{id: 1}
	f, err := fleet.NewFleet(reg, ins, "") // empty ⇒ DefaultQueuePrefix
	require.NoError(t, err)

	_, err = f.Submit(context.Background(), fleet.Request{Model: ref, Capability: "cpu"})
	require.NoError(t, err)
	require.Equal(t, fleet.DefaultQueuePrefix+"-cpu", ins.gotOpts.Queue)
}

func TestSubmit_rejectsMissingCapability(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	f, err := fleet.NewFleet(reg, &fakeInserter{}, "tiny")
	require.NoError(t, err)

	_, err = f.Submit(context.Background(), fleet.Request{Model: ref})
	require.ErrorContains(t, err, "Capability required")
}

func TestSubmit_rejectsMissingModelName(t *testing.T) {
	t.Parallel()
	reg, _ := seedRegistry(t)
	f, err := fleet.NewFleet(reg, &fakeInserter{}, "tiny")
	require.NoError(t, err)

	_, err = f.Submit(context.Background(), fleet.Request{Capability: "cpu"})
	require.ErrorContains(t, err, "Model.Name required")
}

func TestSubmit_rejectsMalformedCapability(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	f, err := fleet.NewFleet(reg, &fakeInserter{}, "tiny")
	require.NoError(t, err)

	_, err = f.Submit(context.Background(), fleet.Request{
		Model:      ref,
		Capability: "gpu.fancy", // "." is rejected by river queue names
	})
	require.ErrorContains(t, err, "must be lowercase")
}

func TestSubmit_normalizesCapabilityCase(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	ins := &fakeInserter{id: 7}
	f, err := fleet.NewFleet(reg, ins, "tiny")
	require.NoError(t, err)

	_, err = f.Submit(context.Background(), fleet.Request{Model: ref, Capability: " GPU "})
	require.NoError(t, err)
	require.Equal(t, "tiny-gpu", ins.gotOpts.Queue)
	args, ok := ins.gotArgs.(fleet.PredictArgs)
	require.True(t, ok)
	require.Equal(t, fleet.Capability("gpu"), args.Capability)
}

func TestSubmit_failsFastOnUnknownModel(t *testing.T) {
	t.Parallel()
	reg := tiny.NewMemoryRegistry() // empty
	f, err := fleet.NewFleet(reg, &fakeInserter{}, "tiny")
	require.NoError(t, err)

	_, err = f.Submit(context.Background(), fleet.Request{
		Model:      tiny.Ref{Name: "nope"},
		Capability: "cpu",
	})
	require.ErrorContains(t, err, "resolve model")
	require.ErrorIs(t, err, tiny.ErrNotFound)
}

func TestSubmit_propagatesInsertError(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	ins := &fakeInserter{err: errors.New("db down")}
	f, err := fleet.NewFleet(reg, ins, "tiny")
	require.NoError(t, err)

	_, err = f.Submit(context.Background(), fleet.Request{Model: ref, Capability: "cpu"})
	require.ErrorContains(t, err, "enqueue")
}

func TestNewFleet_rejectsNilDeps(t *testing.T) {
	t.Parallel()
	reg := tiny.NewMemoryRegistry()
	_, err := fleet.NewFleet(nil, &fakeInserter{}, "tiny")
	require.ErrorContains(t, err, "nil registry")
	_, err = fleet.NewFleet(reg, nil, "tiny")
	require.ErrorContains(t, err, "nil inserter")
}

func TestPredictArgs_kindStable(t *testing.T) {
	t.Parallel()
	require.Equal(t, "golusoris.tiny.fleet.predict", fleet.PredictArgs{}.Kind())
}

// stubPredictor is a tiny.Predictor double recording calls.
type stubPredictor struct {
	loadErr    error
	predictErr error
	out        tiny.Prediction
	closed     bool
	loaded     bool
}

func (s *stubPredictor) Load(context.Context, tiny.Model) error {
	s.loaded = true
	return s.loadErr
}

func (s *stubPredictor) Predict(context.Context, any) (tiny.Prediction, error) {
	if s.predictErr != nil {
		return tiny.Prediction{}, s.predictErr
	}
	return s.out, nil
}

func (s *stubPredictor) Close() error {
	s.closed = true
	return nil
}

// captureSink records the last stored prediction.
type captureSink struct {
	mu  sync.Mutex
	ref tiny.Ref
	p   tiny.Prediction
	err error
	n   int
}

func (c *captureSink) Store(_ context.Context, ref tiny.Ref, p tiny.Prediction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.ref = ref
	c.p = p
	return c.err
}

func newJob(args fleet.PredictArgs) *river.Job[fleet.PredictArgs] {
	return &river.Job[fleet.PredictArgs]{JobRow: &rivertype.JobRow{ID: 1}, Args: args}
}

// isCancel reports whether err is a river.JobCancel (permanent failure).
func isCancel(err error) bool {
	var c *river.JobCancelError
	return errors.As(err, &c)
}

func TestWorker_happyPath(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	pred := &stubPredictor{out: tiny.Prediction{Text: "label-a"}}
	sink := &captureSink{}
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(pred), sink,
		[]fleet.Capability{"cpu"}, time.Second, 0, nil)
	require.NoError(t, err)

	err = w.Work(context.Background(), newJob(fleet.PredictArgs{
		Ref:        ref,
		Capability: "cpu",
		Input:      "hi",
	}))
	require.NoError(t, err)
	require.Equal(t, 1, sink.n)
	require.Equal(t, "label-a", sink.p.Text)
	require.Equal(t, "intent", sink.ref.Name)
	require.True(t, pred.loaded)
}

func TestWorker_capabilityMismatchIsCancel(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(&stubPredictor{}), &captureSink{},
		[]fleet.Capability{"cpu"}, 0, 0, nil)
	require.NoError(t, err)

	err = w.Work(context.Background(), newJob(fleet.PredictArgs{
		Ref:        ref,
		Capability: "gpu", // node only serves cpu
	}))
	require.ErrorContains(t, err, "not served by this node")
	require.True(t, isCancel(err), "capability mismatch must be a permanent cancel")
}

func TestWorker_oversizedInputIsCancel(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(&stubPredictor{}), &captureSink{},
		[]fleet.Capability{"cpu"}, 0, 16, nil) // 16-byte cap
	require.NoError(t, err)

	err = w.Work(context.Background(), newJob(fleet.PredictArgs{
		Ref:        ref,
		Capability: "cpu",
		Input:      strings.Repeat("x", 256),
	}))
	require.ErrorContains(t, err, "exceeds cap")
	require.True(t, isCancel(err), "oversized input must be a permanent cancel")
}

func TestWorker_unknownModelRetries(t *testing.T) {
	t.Parallel()
	reg := tiny.NewMemoryRegistry() // empty
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(&stubPredictor{}), &captureSink{},
		[]fleet.Capability{"cpu"}, 0, 0, nil)
	require.NoError(t, err)

	err = w.Work(context.Background(), newJob(fleet.PredictArgs{
		Ref:        tiny.Ref{Name: "nope"},
		Capability: "cpu",
	}))
	require.ErrorContains(t, err, "resolve model")
	require.False(t, isCancel(err), "unknown model is transient ⇒ retry, not cancel")
}

func TestWorker_predictErrorRetries(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	pred := &stubPredictor{predictErr: errors.New("backend 500")}
	sink := &captureSink{}
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(pred), sink,
		[]fleet.Capability{"cpu"}, 0, 0, nil)
	require.NoError(t, err)

	err = w.Work(context.Background(), newJob(fleet.PredictArgs{Ref: ref, Capability: "cpu"}))
	require.ErrorContains(t, err, "predict")
	require.Equal(t, 0, sink.n) // never stored
}

func TestWorker_loadErrorRetries(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	pred := &stubPredictor{loadErr: errors.New("model not on disk")}
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(pred), &captureSink{},
		[]fleet.Capability{"cpu"}, 0, 0, nil)
	require.NoError(t, err)

	err = w.Work(context.Background(), newJob(fleet.PredictArgs{Ref: ref, Capability: "cpu"}))
	require.ErrorContains(t, err, "load model")
}

func TestWorker_sinkErrorRetries(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	sink := &captureSink{err: errors.New("disk full")}
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(&stubPredictor{}), sink,
		[]fleet.Capability{"cpu"}, 0, 0, nil)
	require.NoError(t, err)

	err = w.Work(context.Background(), newJob(fleet.PredictArgs{Ref: ref, Capability: "cpu"}))
	require.ErrorContains(t, err, "store result")
}

// perModelFactory tracks Close calls to prove the fleet tears down a
// per-job predictor.
func TestWorker_perJobFactoryClosed(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	pred := &stubPredictor{out: tiny.Prediction{Text: "ok"}}
	factory := func(tiny.Model) (tiny.Predictor, error) { return pred, nil }
	w, err := fleet.NewWorker(reg, factory, &captureSink{},
		[]fleet.Capability{"cpu"}, 0, 0, nil)
	require.NoError(t, err)

	require.NoError(t, w.Work(context.Background(), newJob(fleet.PredictArgs{Ref: ref, Capability: "cpu"})))
	require.True(t, pred.closed, "per-job predictor must be Closed")
}

func TestNewWorker_rejectsBadArgs(t *testing.T) {
	t.Parallel()
	reg := tiny.NewMemoryRegistry()
	fac := fleet.SingletonFactory(&stubPredictor{})
	sink := &captureSink{}

	_, err := fleet.NewWorker(nil, fac, sink, []fleet.Capability{"cpu"}, 0, 0, nil)
	require.ErrorContains(t, err, "nil registry")
	_, err = fleet.NewWorker(reg, nil, sink, []fleet.Capability{"cpu"}, 0, 0, nil)
	require.ErrorContains(t, err, "nil predictor factory")
	_, err = fleet.NewWorker(reg, fac, nil, []fleet.Capability{"cpu"}, 0, 0, nil)
	require.ErrorContains(t, err, "nil result sink")
	_, err = fleet.NewWorker(reg, fac, sink, nil, 0, 0, nil)
	require.ErrorContains(t, err, "no capabilities")
	// blank-only caps normalize to empty ⇒ rejected.
	_, err = fleet.NewWorker(reg, fac, sink, []fleet.Capability{"  ", ""}, 0, 0, nil)
	require.ErrorContains(t, err, "no capabilities")
	// malformed (non-blank) capability ⇒ rejected loudly.
	_, err = fleet.NewWorker(reg, fac, sink, []fleet.Capability{"gpu/v2"}, 0, 0, nil)
	require.ErrorContains(t, err, "must be lowercase")
}

func TestWorker_capabilityNormalization(t *testing.T) {
	t.Parallel()
	reg, ref := seedRegistry(t)
	sink := &captureSink{}
	// Node declares "GPU" (mixed case + dupes); a job tagged lowercase
	// "gpu" must still match after normalization.
	w, err := fleet.NewWorker(reg, fleet.SingletonFactory(&stubPredictor{out: tiny.Prediction{Text: "y"}}),
		sink, []fleet.Capability{"GPU", "gpu", " Gpu "}, 0, 0, nil)
	require.NoError(t, err)

	require.NoError(t, w.Work(context.Background(), newJob(fleet.PredictArgs{Ref: ref, Capability: "gpu"})))
	require.Equal(t, 1, sink.n)
}

func TestResultSinkFunc_adapts(t *testing.T) {
	t.Parallel()
	var got tiny.Prediction
	var sink fleet.ResultSink = fleet.ResultSinkFunc(func(_ context.Context, _ tiny.Ref, p tiny.Prediction) error {
		got = p
		return nil
	})
	require.NoError(t, sink.Store(context.Background(), tiny.Ref{}, tiny.Prediction{Text: "z"}))
	require.Equal(t, "z", got.Text)
}
