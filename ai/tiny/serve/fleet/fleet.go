// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package fleet is the distributed-inference recipe for [ai/tiny]: it
// serves a [tiny.Predictor] across a replica set using the framework's
// own [jobs] (river) queue + [leader] election, instead of a
// hand-rolled controller.
//
// Topology:
//
//   - A controller (any replica) calls [Fleet.Submit] to enqueue a
//     prediction. Submit resolves the [tiny.Ref] against the
//     [tiny.Registry] so a bad model fails fast, then inserts a
//     [PredictArgs] river job into a capability-matched queue.
//   - Worker nodes register a [Worker] (via [Module]) on exactly the
//     queues whose capability they possess. river fetches each job only
//     onto a node that subscribed to its queue — that IS the
//     capability-matched scheduler, with no bespoke SQLite controller.
//   - The worker resolves the model, loads a [tiny.Predictor] (chosen
//     per [tiny.Format] by a [PredictorFactory]), runs Predict, and
//     hands the [tiny.Prediction] to a [ResultSink].
//
// Why this over vmafx's controller: golusoris already ships durable
// queues (river/Postgres), graceful drain, retries, and leader election.
// A capability is just a river queue name; node fan-out is river's
// fetch model. Apps get distributed inference by composing existing
// modules.
//
// Config keys (env: APP_TINY_FLEET_*):
//
//	tiny.fleet.enabled                 # master switch (default true)
//	tiny.fleet.queue_prefix            # queue-name prefix (default "tiny")
//	tiny.fleet.capabilities            # this node's capabilities (default ["cpu"])
//	tiny.fleet.max_workers             # per-capability max concurrent workers (default 4)
//	tiny.fleet.predict_timeout         # per-prediction cap (default 60s)
//	tiny.fleet.max_input_bytes         # cap on a job's encoded input (default 1 MiB)
package fleet

import (
	"context"
	jsonv1 "encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/core/validate"
)

// capabilityRe restricts a (normalized) capability to river's queue-name
// charset so "<prefix>-<capability>" is always a valid queue name:
// lowercase letters/digits separated by "_" or "-".
var capabilityRe = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*$`)

// DefaultQueuePrefix is prepended to a capability to form the river
// queue name (e.g. capability "gpu" → queue "tiny-gpu"). river requires
// queue names of lowercase letters/digits separated by "_" or "-", so
// the prefix + capability are joined with "-".
const DefaultQueuePrefix = "tiny"

// queueSep joins the prefix and capability. river rejects "." in queue
// names, so a hyphen is used.
const queueSep = "-"

// maxQueueNameBytes mirrors River v0.49.0's queue-name limit.
const maxQueueNameBytes = 64

// DefaultMaxInputBytes caps the JSON-encoded job input. Tiny task
// models take short prompts / small feature vectors; 1 MiB is generous
// and bounds the river row size + decode cost.
const DefaultMaxInputBytes = 1 << 20

// Capability is a node trait a prediction can require — "cpu", "gpu",
// "onnx", "tflite", an accelerator SKU, etc. It is opaque to the fleet;
// apps define their own vocabulary. The empty capability is invalid.
type Capability string

// Request is a prediction asked of the fleet via [Fleet.Submit].
type Request struct {
	// Model selects the registered model. Version 0 means "latest".
	Model tiny.Ref
	// Capability is the node trait required to serve this request. It
	// maps directly to a river queue, so only matching nodes fetch it.
	Capability Capability
	// Input is the predictor input. It must JSON-encode (it crosses the
	// queue as a river job arg); the chosen Predictor documents the
	// concrete type it expects after decode (string for generate, etc.).
	Input any
	// Priority is the river job priority (1 = highest, 4 = lowest;
	// 0 ⇒ river default of 1).
	Priority int
	// Tags are attached to the river job for observability.
	Tags []string
}

// queueName maps a capability to its bounded River queue under prefix.
func queueName(prefix string, c Capability) (string, error) {
	name := prefix + queueSep + string(c)
	if len(name) > maxQueueNameBytes {
		return "", fmt.Errorf(
			"ai/tiny/serve/fleet: queue name %q exceeds River's %d bytes",
			name,
			maxQueueNameBytes,
		)
	}
	return name, nil
}

func normalizeQueuePrefix(prefix string) (string, error) {
	if prefix == "" {
		prefix = DefaultQueuePrefix
	}
	normalized := strings.ToLower(strings.TrimSpace(prefix))
	if normalized == "" || !capabilityRe.MatchString(normalized) {
		return "", fmt.Errorf(
			"ai/tiny/serve/fleet: queue prefix %q must be lowercase letters/digits separated by - or _",
			prefix,
		)
	}
	if len(normalized)+len(queueSep)+1 > maxQueueNameBytes {
		return "", fmt.Errorf(
			"ai/tiny/serve/fleet: queue prefix %q leaves no room within River's %d-byte queue limit",
			prefix,
			maxQueueNameBytes,
		)
	}
	return normalized, nil
}

// normalizeCapability lowercases + trims a capability and validates it
// against river's queue-name charset.
func normalizeCapability(c Capability) (Capability, error) {
	n := Capability(strings.ToLower(strings.TrimSpace(string(c))))
	if n == "" {
		return "", errors.New("ai/tiny/serve/fleet: empty capability")
	}
	if !capabilityRe.MatchString(string(n)) {
		return "", fmt.Errorf("ai/tiny/serve/fleet: capability %q must be lowercase letters/digits separated by - or _", c)
	}
	return n, nil
}

// PredictArgs is the river job payload for one prediction. It is the
// wire contract between controller (Submit) and worker (Work).
type PredictArgs struct {
	// Ref identifies the model to load (Name/TenantID/Version).
	Ref tiny.Ref `json:"ref"`
	// Capability echoes Request.Capability for audit + worker re-check.
	Capability Capability `json:"capability"`
	// Input is the raw predictor input, re-decoded by the worker.
	Input any `json:"input"`
}

// Kind implements river.JobArgs. The kind is stable so workers across
// versions agree on the payload.
func (PredictArgs) Kind() string { return "golusoris.tiny.fleet.predict" }

// ResultSink receives a finished prediction. Implementations persist or
// forward it (DB row, cache, pub/sub, webhook). The fleet calls Store
// exactly once per successfully-worked job; a Store error fails the job
// so river retries per its backoff.
type ResultSink interface {
	Store(ctx context.Context, ref tiny.Ref, p tiny.Prediction) error
}

// ResultSinkFunc adapts a function to [ResultSink].
type ResultSinkFunc func(ctx context.Context, ref tiny.Ref, p tiny.Prediction) error

// Store calls the wrapped function.
func (f ResultSinkFunc) Store(ctx context.Context, ref tiny.Ref, p tiny.Prediction) error {
	return f(ctx, ref, p)
}

// PredictorFactory builds a fresh [tiny.Predictor] for a model. The
// fleet calls it per job and Closes the predictor when the job ends. Returning
// a nil predictor with a nil error is a programming bug and is rejected.
type PredictorFactory func(m tiny.Model) (tiny.Predictor, error)

// SingletonFactory returns a [PredictorFactory] backed by one process-wide
// predictor. Each returned lease serializes its Load+Predict pair so one job
// cannot replace the model another in-flight job is about to use.
func SingletonFactory(p tiny.Predictor) PredictorFactory {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return func(tiny.Model) (tiny.Predictor, error) {
		if validate.IsNil(p) {
			return nil, errors.New("ai/tiny/serve/fleet: singleton has nil predictor")
		}
		return &singletonLease{Predictor: p, gate: gate}, nil
	}
}

// singletonLease keeps one model selection stable through its prediction and
// returns the shared predictor to the factory without closing it.
type singletonLease struct {
	tiny.Predictor
	gate     chan struct{}
	acquired bool
}

func (p *singletonLease) Load(ctx context.Context, model tiny.Model) error {
	if p.acquired {
		return errors.New("ai/tiny/serve/fleet: singleton lease already loaded")
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("ai/tiny/serve/fleet: acquire singleton predictor: %w", ctx.Err())
	case <-p.gate:
		p.acquired = true
	}
	loaded := false
	defer func() {
		if !loaded {
			p.release()
		}
	}()
	if err := p.Predictor.Load(ctx, model); err != nil {
		return fmt.Errorf("ai/tiny/serve/fleet: load singleton predictor: %w", err)
	}
	loaded = true
	return nil
}

func (p *singletonLease) Predict(ctx context.Context, input any) (tiny.Prediction, error) {
	if !p.acquired {
		return tiny.Prediction{}, errors.New("ai/tiny/serve/fleet: singleton predictor not loaded")
	}
	defer p.release()
	prediction, err := p.Predictor.Predict(ctx, input)
	if err != nil {
		return tiny.Prediction{}, fmt.Errorf("ai/tiny/serve/fleet: predict with singleton: %w", err)
	}
	return prediction, nil
}

func (p *singletonLease) Close() error {
	p.release()
	return nil
}

func (p *singletonLease) release() {
	if p.acquired {
		p.acquired = false
		p.gate <- struct{}{}
	}
}

// Inserter is the subset of [river.Client] the controller needs. Both
// the real *river.Client[pgx.Tx] and test doubles satisfy it.
type Inserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Fleet is the controller handle. It validates + enqueues predictions;
// the work happens on a node running [Worker].
type Fleet struct {
	registry      tiny.Registry
	inserter      Inserter
	queuePrefix   string
	maxInputBytes int
}

// NewFleet builds a controller. registry resolves models pre-flight;
// inserter enqueues onto a capability queue. queuePrefix defaults to
// [DefaultQueuePrefix] when empty.
func NewFleet(registry tiny.Registry, inserter Inserter, queuePrefix string) (*Fleet, error) {
	return NewFleetWithInputLimit(registry, inserter, queuePrefix, DefaultMaxInputBytes)
}

// NewFleetWithInputLimit builds a controller with an explicit encoded-input
// cap. Non-positive limits select [DefaultMaxInputBytes].
func NewFleetWithInputLimit(
	registry tiny.Registry, inserter Inserter, queuePrefix string, maxInputBytes int,
) (*Fleet, error) {
	if validate.IsNil(registry) {
		return nil, errors.New("ai/tiny/serve/fleet: nil registry")
	}
	if validate.IsNil(inserter) {
		return nil, errors.New("ai/tiny/serve/fleet: nil inserter")
	}
	normalizedPrefix, err := normalizeQueuePrefix(queuePrefix)
	if err != nil {
		return nil, err
	}
	if maxInputBytes <= 0 {
		maxInputBytes = DefaultMaxInputBytes
	}
	return &Fleet{
		registry: registry, inserter: inserter, queuePrefix: normalizedPrefix, maxInputBytes: maxInputBytes,
	}, nil
}

// Submit validates req, resolves the model, and enqueues a prediction
// onto the capability's queue. It returns the inserted river job ID.
// A model that does not resolve fails here (fast) rather than on a node.
func (f *Fleet) Submit(ctx context.Context, req Request) (int64, error) {
	if req.Capability == "" {
		return 0, errors.New("ai/tiny/serve/fleet: Request.Capability required")
	}
	if req.Model.Name == "" {
		return 0, errors.New("ai/tiny/serve/fleet: Request.Model.Name required")
	}
	capName, err := normalizeCapability(req.Capability)
	if err != nil {
		return 0, err
	}
	if err = checkInputSize(req.Input, f.maxInputBytes); err != nil {
		return 0, err
	}
	// Pre-flight resolve: fail fast on an unknown / not-yet-trained model
	// instead of burning a queue round-trip + a node's load attempt.
	model, getErr := f.registry.GetModel(ctx, req.Model)
	if getErr != nil {
		return 0, fmt.Errorf("ai/tiny/serve/fleet: resolve model: %w", getErr)
	}
	resolvedRef, refErr := resolvedModelRef(model)
	if refErr != nil {
		return 0, refErr
	}
	queue, queueErr := queueName(f.queuePrefix, capName)
	if queueErr != nil {
		return 0, queueErr
	}
	opts := &river.InsertOpts{
		Queue:    queue,
		Priority: req.Priority,
		Tags:     slices.Clone(req.Tags),
	}
	res, err := f.inserter.Insert(ctx, PredictArgs{
		Ref:        resolvedRef,
		Capability: capName,
		Input:      req.Input,
	}, opts)
	if err != nil {
		return 0, fmt.Errorf("ai/tiny/serve/fleet: enqueue: %w", err)
	}
	return res.Job.ID, nil
}

func resolvedModelRef(model tiny.Model) (tiny.Ref, error) {
	if model.Name == "" || model.Version < 1 {
		return tiny.Ref{}, fmt.Errorf(
			"ai/tiny/serve/fleet: registry returned unresolved model name=%q version=%d",
			model.Name, model.Version,
		)
	}
	return tiny.Ref{Name: model.Name, TenantID: model.TenantID, Version: model.Version}, nil
}

func checkInputSize(input any, maxInputBytes int) error {
	if maxInputBytes <= 0 {
		return errors.New("ai/tiny/serve/fleet: input cap must be positive")
	}
	if err := validateJSONInputMemory(input, maxInputBytes); err != nil {
		return err
	}
	w := jsonLimitWriter{limit: int64(maxInputBytes)}
	err := jsonv2.MarshalWrite(&w, input, jsonv1.DefaultOptionsV1())
	if errors.Is(err, errInputTooLarge) {
		return fmt.Errorf(
			"ai/tiny/serve/fleet: input exceeds cap %d bytes",
			maxInputBytes,
		)
	}
	if err != nil {
		return fmt.Errorf("ai/tiny/serve/fleet: encode input: %w", err)
	}
	return nil
}

var errInputTooLarge = errors.New("encoded input exceeds limit")

type jsonLimitWriter struct {
	limit   int64
	written int64
}

func (w *jsonLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-w.written {
		return 0, errInputTooLarge
	}
	w.written += int64(len(p))
	return len(p), nil
}

// normalizeCaps validates, lowercases, de-dupes, and sorts caps so a
// node's queue set is deterministic. Blank entries are dropped; an
// otherwise-malformed capability is an error. Returns an error when
// nothing valid is left (a node with no capability can serve nothing).
func normalizeCaps(caps []Capability) ([]Capability, error) {
	seen := make(map[Capability]struct{}, len(caps))
	out := make([]Capability, 0, len(caps))
	for _, c := range caps {
		// Drop blank/whitespace-only entries silently (config padding);
		// reject non-blank-but-malformed loudly.
		if strings.TrimSpace(string(c)) == "" {
			continue
		}
		n, err := normalizeCapability(c)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, errors.New("ai/tiny/serve/fleet: node has no capabilities")
	}
	slices.Sort(out)
	return out, nil
}
