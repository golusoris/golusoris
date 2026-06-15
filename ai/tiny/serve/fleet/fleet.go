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
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/golusoris/golusoris/ai/tiny"
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

// queueName maps a capability to its river queue under prefix.
func queueName(prefix string, c Capability) string {
	return prefix + queueSep + string(c)
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
// fleet calls it per job and Closes the predictor when the job ends, so
// the factory can return a cheap per-model client (the ollama Predictor
// is a stateless HTTP client). Returning a nil predictor with a nil
// error is a programming bug and is rejected.
type PredictorFactory func(m tiny.Model) (tiny.Predictor, error)

// SingletonFactory returns a [PredictorFactory] that always hands back
// p and a no-op Close, for predictors that are already concurrency-safe
// and process-wide (e.g. one ollama client serving every model).
func SingletonFactory(p tiny.Predictor) PredictorFactory {
	return func(tiny.Model) (tiny.Predictor, error) {
		return noClosePredictor{p}, nil
	}
}

// noClosePredictor wraps a shared Predictor so the fleet's per-job Close
// does not tear down a process-wide instance.
type noClosePredictor struct{ tiny.Predictor }

func (noClosePredictor) Close() error { return nil }

// Inserter is the subset of [river.Client] the controller needs. Both
// the real *river.Client[pgx.Tx] and test doubles satisfy it.
type Inserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Fleet is the controller handle. It validates + enqueues predictions;
// the work happens on a node running [Worker].
type Fleet struct {
	registry    tiny.Registry
	inserter    Inserter
	queuePrefix string
}

// NewFleet builds a controller. registry resolves models pre-flight;
// inserter enqueues onto a capability queue. queuePrefix defaults to
// [DefaultQueuePrefix] when empty.
func NewFleet(registry tiny.Registry, inserter Inserter, queuePrefix string) (*Fleet, error) {
	if registry == nil {
		return nil, errors.New("ai/tiny/serve/fleet: nil registry")
	}
	if inserter == nil {
		return nil, errors.New("ai/tiny/serve/fleet: nil inserter")
	}
	if queuePrefix == "" {
		queuePrefix = DefaultQueuePrefix
	}
	return &Fleet{registry: registry, inserter: inserter, queuePrefix: queuePrefix}, nil
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
	// Pre-flight resolve: fail fast on an unknown / not-yet-trained model
	// instead of burning a queue round-trip + a node's load attempt.
	if _, gErr := f.registry.GetModel(ctx, req.Model); gErr != nil {
		return 0, fmt.Errorf("ai/tiny/serve/fleet: resolve model: %w", gErr)
	}
	opts := &river.InsertOpts{
		Queue:    queueName(f.queuePrefix, capName),
		Priority: req.Priority,
		Tags:     req.Tags,
	}
	res, err := f.inserter.Insert(ctx, PredictArgs{
		Ref:        req.Model,
		Capability: capName,
		Input:      req.Input,
	}, opts)
	if err != nil {
		return 0, fmt.Errorf("ai/tiny/serve/fleet: enqueue: %w", err)
	}
	return res.Job.ID, nil
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
