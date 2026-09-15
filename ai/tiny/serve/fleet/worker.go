// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/ai/tiny"
)

// Worker is the node side of the fleet. It implements river's
// Worker[PredictArgs]: resolve the model, load a predictor, run the
// prediction, persist the result. One Worker handles every capability
// queue this node subscribes to — capability matching already happened
// at fetch time (river only hands it jobs from its queues).
type Worker struct {
	river.WorkerDefaults[PredictArgs]

	registry tiny.Registry
	factory  PredictorFactory
	sink     ResultSink
	logger   *slog.Logger
	// caps is the set this node can serve; a job whose capability is not
	// in caps is a routing bug and is discarded (not retried).
	caps map[Capability]struct{}
	// timeout caps a single Predict call (0 ⇒ defer to river JobTimeout).
	timeout time.Duration
	// maxInputBytes bounds the re-encoded job input.
	maxInputBytes int
}

// NewWorker builds the node-side worker. registry resolves the model,
// factory builds a per-job predictor, sink stores the result. caps is
// the capability set this node serves.
func NewWorker(
	registry tiny.Registry,
	factory PredictorFactory,
	sink ResultSink,
	caps []Capability,
	timeout time.Duration,
	maxInputBytes int,
	logger *slog.Logger,
) (*Worker, error) {
	if registry == nil {
		return nil, errors.New("ai/tiny/serve/fleet: nil registry")
	}
	if factory == nil {
		return nil, errors.New("ai/tiny/serve/fleet: nil predictor factory")
	}
	if sink == nil {
		return nil, errors.New("ai/tiny/serve/fleet: nil result sink")
	}
	norm, err := normalizeCaps(caps)
	if err != nil {
		return nil, err
	}
	if maxInputBytes <= 0 {
		maxInputBytes = DefaultMaxInputBytes
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	set := make(map[Capability]struct{}, len(norm))
	for _, c := range norm {
		set[c] = struct{}{}
	}
	return &Worker{
		registry:      registry,
		factory:       factory,
		sink:          sink,
		logger:        logger,
		caps:          set,
		timeout:       timeout,
		maxInputBytes: maxInputBytes,
	}, nil
}

// Work runs one prediction. A capability mismatch or oversized input is
// a permanent failure (river.JobCancel) — retrying won't help. Transient
// failures (load, predict, sink) return a plain error so river retries.
func (w *Worker) Work(ctx context.Context, job *river.Job[PredictArgs]) error {
	args := job.Args
	if err := w.checkJob(args); err != nil {
		return err
	}
	model, pred, err := w.resolvePredictor(ctx, args)
	if err != nil {
		return err
	}
	defer w.closePredictor(ctx, pred, model.Name)

	predCtx, cancelPred := withWorkTimeout(ctx, w.timeout)
	defer cancelPred()
	out, err := w.predict(predCtx, pred, model, args)
	if err != nil {
		return err
	}
	if sErr := w.sink.Store(ctx, args.Ref, out); sErr != nil {
		return fmt.Errorf("ai/tiny/serve/fleet: store result: %w", sErr)
	}
	w.logger.DebugContext(ctx, "ai/tiny/serve/fleet: prediction done",
		slog.String("model", model.Name),
		slog.Int("version", model.Version),
		slog.String("capability", string(args.Capability)))
	return nil
}

// checkJob verifies that args.Capability is served by this node and that
// its input fits maxInputBytes. Both failures are permanent (cancel, no
// retry): a routing bug or an oversized payload won't fix itself.
func (w *Worker) checkJob(args PredictArgs) error {
	if _, ok := w.caps[args.Capability]; !ok {
		return cancel(fmt.Errorf("ai/tiny/serve/fleet: capability %q not served by this node", args.Capability))
	}
	if err := w.checkInputSize(args.Input); err != nil {
		return cancel(err)
	}
	return nil
}

// resolvePredictor loads the target model and builds a per-job predictor
// for it via w.factory.
func (w *Worker) resolvePredictor(ctx context.Context, args PredictArgs) (tiny.Model, tiny.Predictor, error) {
	model, err := w.registry.GetModel(ctx, args.Ref)
	if err != nil {
		return tiny.Model{}, nil, fmt.Errorf("ai/tiny/serve/fleet: resolve model %s: %w", args.Ref.Name, err)
	}
	pred, err := w.factory(model)
	if err != nil {
		return tiny.Model{}, nil, fmt.Errorf("ai/tiny/serve/fleet: build predictor: %w", err)
	}
	if pred == nil {
		return tiny.Model{}, nil, errors.New("ai/tiny/serve/fleet: factory returned nil predictor")
	}
	return model, pred, nil
}

// closePredictor closes pred, logging (not failing the job on) any error:
// by the time this runs the prediction has already succeeded or failed,
// so a close failure must not overturn that outcome.
func (w *Worker) closePredictor(ctx context.Context, pred tiny.Predictor, modelName string) {
	if cErr := pred.Close(); cErr != nil {
		w.logger.WarnContext(ctx, "ai/tiny/serve/fleet: predictor close failed",
			slog.String("model", modelName), slog.String("error", cErr.Error()))
	}
}

// withWorkTimeout derives a child context bounded by timeout, or returns
// ctx unchanged (with a no-op cancel) when timeout is 0, deferring to
// river's own JobTimeout.
func withWorkTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// predict loads model into pred, then runs the prediction on args.Input.
func (w *Worker) predict(ctx context.Context, pred tiny.Predictor, model tiny.Model, args PredictArgs) (tiny.Prediction, error) {
	if lErr := pred.Load(ctx, model); lErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/fleet: load model %s: %w", model.Name, lErr)
	}
	out, pErr := pred.Predict(ctx, args.Input)
	if pErr != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/fleet: predict %s: %w", model.Name, pErr)
	}
	return out, nil
}

// cancel marks err as a permanent river job cancellation. The cancel
// wrapper must be returned as-is so river's errors.As detects it; further
// wrapping would defeat that, so wrapcheck is suppressed here.
func cancel(err error) error {
	return river.JobCancel(err) //nolint:wrapcheck // river.JobCancel sentinel must reach river unwrapped
}

// checkInputSize bounds the re-encoded input so a malicious / buggy
// producer can't push an unbounded payload through the worker. river
// already stored the row; this is a defense-in-depth decode cap.
func (w *Worker) checkInputSize(input any) error {
	if input == nil {
		return nil
	}
	b, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("ai/tiny/serve/fleet: encode input: %w", err)
	}
	if len(b) > w.maxInputBytes {
		return fmt.Errorf("ai/tiny/serve/fleet: input %d bytes exceeds cap %d", len(b), w.maxInputBytes)
	}
	return nil
}
