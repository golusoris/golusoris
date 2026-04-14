// Package tiny is a framework for training and serving small
// task-specific models — text/image/audio classifiers, embedding
// extractors, and tiny generative LMs (e.g. Gemma 3 270M/1B, Gemma 3n
// E2B) fine-tuned on a single job.
//
// Go orchestrates; Python does the heavy lifting. Each [Trainer]
// implementation spawns a container (docker/k8s Job) with the right
// Python toolchain pinned — KerasNLP + LoRA for Gemma, MediaPipe Model
// Maker for LiteRT classifiers — and captures the trained artifact
// into a [Registry] for later serving via [Predictor].
//
// The umbrella interfaces live here; concrete trainers are in
// sibling packages:
//
//   - ai/tiny/gemma/   — Gemma LoRA fine-tuning (text generation)
//   - ai/tiny/litert/  — MediaPipe Model Maker (classifiers + embeddings)
//   - ai/tiny/serve/   — inference adapters (ollama for Gemma, tflite for LiteRT)
package tiny

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/id"
)

// Modality is the input shape the model consumes.
type Modality string

// Supported modalities.
const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
)

// TaskKind is what the trained model does.
type TaskKind string

// Supported task kinds.
const (
	TaskGenerate TaskKind = "generate" // text → text (Gemma fine-tune)
	TaskClassify TaskKind = "classify" // input → label + probabilities (LiteRT)
	TaskRegress  TaskKind = "regress"  // input → float
	TaskEmbed    TaskKind = "embed"    // input → []float32
)

// Format is the on-disk artifact format.
type Format string

// Known artifact formats.
const (
	FormatTFLite    Format = "tflite"     // MediaPipe / LiteRT
	FormatKerasLoRA Format = "keras-lora" // KerasNLP LoRA weights
	FormatGGUF      Format = "gguf"       // llama.cpp / ollama
	FormatONNX      Format = "onnx"
)

// Dataset references a materialized training corpus. The
// [Dataset.URI] is resolvable by the chosen [Trainer] (typically an
// object-storage path the Trainer's container mounts or pulls).
type Dataset struct {
	ID       string
	URI      string // e.g. "s3://bucket/datasets/intent-v3.jsonl" or "file:///tmp/ds.csv"
	Format   string // "jsonl", "csv", "imagefolder", …
	Modality Modality
	TaskKind TaskKind
	Size     int64 // bytes (0 when unknown)
	Examples int   // row count (0 when unknown)
	// SchemaHint is stack-specific. For JSONL chat fine-tune it names
	// the prompt/response columns; for classifier datasets it lists the
	// label set. Trainers document the shape they expect.
	SchemaHint map[string]any
}

// Job describes a training run that has not yet produced a [Model].
type Job struct {
	ID        string
	Name      string // short human-readable ("support-intent-v3")
	TenantID  string // optional multi-tenant scope
	Dataset   Dataset
	BaseModel string // Trainer-specific ("gemma3:270m", "mediapipe/text_classifier")
	// Hyperparams is free-form; each Trainer documents the keys it
	// honours (lr, epochs, batch_size, lora_rank, …).
	Hyperparams map[string]any
	Tags        map[string]string
	CreatedAt   time.Time
}

// Model is a trained artifact produced by a [Trainer.Train] call and
// persisted via a [Registry].
type Model struct {
	ID        string
	JobID     string
	Name      string // matches Job.Name — the logical model identity
	TenantID  string
	Version   int    // monotonic per (TenantID, Name); assigned by Registry
	URI       string // s3://… / file:///… where the artifact lives
	Format    Format
	Modality  Modality
	TaskKind  TaskKind
	BaseModel string
	// Labels is the ordered label set for classifiers (index = logit).
	Labels []string
	// Metrics captured at training end (accuracy, f1, loss, …).
	Metrics   map[string]float64
	Metadata  map[string]string
	CreatedAt time.Time
}

// Ref points to a registered [Model] by name + version. Version 0 means
// "latest".
type Ref struct {
	Name     string
	TenantID string
	Version  int
}

// Trainer runs a [Job] and produces a [Model]. Implementations are
// responsible for uploading the artifact to durable storage before
// returning; the URI on the returned Model must be stable.
type Trainer interface {
	// Name reports the trainer name ("gemma", "litert") — used for logs
	// and registry filtering.
	Name() string
	// Train blocks until the job finishes or ctx is canceled. A partial
	// failure should return a non-nil error with the Model zero-valued;
	// the caller MUST NOT persist a zero Model.
	Train(ctx context.Context, job Job) (Model, error)
}

// Prediction is the output of [Predictor.Predict].
type Prediction struct {
	Labels    []LabelScore // classifier output (sorted desc by Score)
	Embedding []float32    // embed output
	Text      string       // generate output
	Raw       any          // trainer-specific escape hatch
}

// LabelScore is one entry in a classifier output.
type LabelScore struct {
	Label string
	Score float32
}

// Predictor loads a [Model] and answers predictions. Implementations
// should be safe for concurrent Predict calls after Load returns.
type Predictor interface {
	Load(ctx context.Context, m Model) error
	Predict(ctx context.Context, input any) (Prediction, error)
	Close() error
}

// ListFilter narrows a [Registry.List] query.
type ListFilter struct {
	TenantID string
	Name     string   // exact match; empty = any
	TaskKind TaskKind // empty = any
	Limit    int      // 0 = no cap
}

// ErrNotFound is returned by [Registry] when a lookup misses.
var ErrNotFound = errors.New("ai/tiny: not found")

// Registry persists [Job] and [Model] records. Implementations must
// assign Model.Version monotonically per (TenantID, Name).
type Registry interface {
	SaveJob(ctx context.Context, j Job) error
	GetJob(ctx context.Context, id string) (Job, error)
	SaveModel(ctx context.Context, m *Model) error // mutates m.Version + m.ID when zero
	GetModel(ctx context.Context, r Ref) (Model, error)
	Latest(ctx context.Context, tenantID, name string) (Model, error)
	List(ctx context.Context, f ListFilter) ([]Model, error)
}

// MemoryRegistry is an in-process [Registry] suitable for tests and
// local development. Not durable.
type MemoryRegistry struct {
	mu     sync.RWMutex
	jobs   map[string]Job
	models map[string]Model // keyed by Model.ID
	idGen  id.Generator
	clk    clockwork.Clock
}

// NewMemoryRegistry returns a MemoryRegistry using the real clock.
func NewMemoryRegistry() *MemoryRegistry {
	return NewMemoryRegistryWithClock(clockwork.NewRealClock())
}

// NewMemoryRegistryWithClock returns a MemoryRegistry with an injected clock.
func NewMemoryRegistryWithClock(clk clockwork.Clock) *MemoryRegistry {
	return &MemoryRegistry{
		jobs:   make(map[string]Job),
		models: make(map[string]Model),
		idGen:  id.New(),
		clk:    clk,
	}
}

// SaveJob stores j, assigning ID + CreatedAt when unset.
func (r *MemoryRegistry) SaveJob(_ context.Context, j Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j.ID == "" {
		j.ID = r.idGen.NewUUID().String()
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = r.clk.Now().UTC()
	}
	r.jobs[j.ID] = j
	return nil
}

// GetJob looks up a Job by ID.
func (r *MemoryRegistry) GetJob(_ context.Context, jobID string) (Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[jobID]
	if !ok {
		return Job{}, fmt.Errorf("ai/tiny: job %q: %w", jobID, ErrNotFound)
	}
	return j, nil
}

// SaveModel assigns ID + Version + CreatedAt as needed and stores m.
// Version is allocated as (max existing version for (TenantID, Name)) + 1.
func (r *MemoryRegistry) SaveModel(_ context.Context, m *Model) error {
	if m == nil {
		return errors.New("ai/tiny: nil model")
	}
	if m.Name == "" {
		return errors.New("ai/tiny: model.Name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.ID == "" {
		m.ID = r.idGen.NewUUID().String()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = r.clk.Now().UTC()
	}
	if m.Version == 0 {
		maxV := 0
		for _, existing := range r.models {
			if existing.TenantID == m.TenantID && existing.Name == m.Name && existing.Version > maxV {
				maxV = existing.Version
			}
		}
		m.Version = maxV + 1
	}
	r.models[m.ID] = *m
	return nil
}

// GetModel looks up a model by Ref. Version 0 resolves to the latest.
func (r *MemoryRegistry) GetModel(ctx context.Context, ref Ref) (Model, error) {
	if ref.Version == 0 {
		return r.Latest(ctx, ref.TenantID, ref.Name)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.models {
		if m.TenantID == ref.TenantID && m.Name == ref.Name && m.Version == ref.Version {
			return m, nil
		}
	}
	return Model{}, fmt.Errorf("ai/tiny: model %s v%d: %w", ref.Name, ref.Version, ErrNotFound)
}

// Latest returns the highest-versioned model for (tenantID, name).
func (r *MemoryRegistry) Latest(_ context.Context, tenantID, name string) (Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var best Model
	found := false
	for _, m := range r.models {
		if m.TenantID != tenantID || m.Name != name {
			continue
		}
		if !found || m.Version > best.Version {
			best = m
			found = true
		}
	}
	if !found {
		return Model{}, fmt.Errorf("ai/tiny: latest %q: %w", name, ErrNotFound)
	}
	return best, nil
}

// List returns models matching f, sorted by (CreatedAt desc, Version desc).
func (r *MemoryRegistry) List(_ context.Context, f ListFilter) ([]Model, error) {
	r.mu.RLock()
	out := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		if f.TenantID != "" && m.TenantID != f.TenantID {
			continue
		}
		if f.Name != "" && m.Name != f.Name {
			continue
		}
		if f.TaskKind != "" && m.TaskKind != f.TaskKind {
			continue
		}
		out = append(out, m)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].Version > out[j].Version
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// ValidateJob performs cheap schema checks on j before handing it to a
// [Trainer]. Use this as a pre-flight so Trainer containers don't
// start just to fail.
func ValidateJob(j Job) error {
	if j.Name == "" {
		return errors.New("ai/tiny: job.Name required")
	}
	if j.Dataset.URI == "" {
		return errors.New("ai/tiny: job.Dataset.URI required")
	}
	if j.Dataset.Modality == "" {
		return errors.New("ai/tiny: job.Dataset.Modality required")
	}
	if j.Dataset.TaskKind == "" {
		return errors.New("ai/tiny: job.Dataset.TaskKind required")
	}
	if j.BaseModel == "" {
		return errors.New("ai/tiny: job.BaseModel required")
	}
	return nil
}
