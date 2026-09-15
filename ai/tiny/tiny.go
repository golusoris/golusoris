// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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

	"github.com/golusoris/golusoris/core/id"
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
	ID       string   `json:"id,omitempty"`
	URI      string   `json:"uri"`              // e.g. "s3://bucket/datasets/intent-v3.jsonl" or "file:///tmp/ds.csv"
	Format   string   `json:"format,omitempty"` // "jsonl", "csv", "imagefolder", …
	Modality Modality `json:"modality"`
	TaskKind TaskKind `json:"task_kind"`
	Size     int64    `json:"size,omitempty"`     // bytes (0 when unknown)
	Examples int      `json:"examples,omitempty"` // row count (0 when unknown)
	// SchemaHint is stack-specific. For JSONL chat fine-tune it names
	// the prompt/response columns; for classifier datasets it lists the
	// label set. Trainers document the shape they expect.
	SchemaHint map[string]any `json:"schema_hint,omitempty"`
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

// validateModelForSave checks the fields every [Registry] implementation's
// SaveModel needs before assigning defaults or persisting.
func validateModelForSave(m *Model) error {
	if m == nil {
		return errors.New("ai/tiny: nil model")
	}
	if m.Name == "" {
		return errors.New("ai/tiny: model.Name required")
	}
	return nil
}

// ensureModelDefaults assigns ID and CreatedAt on m when unset, using gen
// for a fresh UUID and clk for the current time — shared by every
// [Registry] implementation's SaveModel ([MemoryRegistry], [PGRegistry]).
func ensureModelDefaults(gen id.Generator, clk clockwork.Clock, m *Model) error {
	if m.ID == "" {
		u, err := gen.NewUUID()
		if err != nil {
			return fmt.Errorf("ai/tiny: model id: %w", err)
		}
		m.ID = u.String()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = clk.Now().UTC()
	}
	return nil
}

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
		u, err := r.idGen.NewUUID()
		if err != nil {
			return fmt.Errorf("ai/tiny: job id: %w", err)
		}
		j.ID = u.String()
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
	if err := validateModelForSave(m); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ensureModelDefaults(r.idGen, r.clk, m); err != nil {
		return err
	}
	if m.Version == 0 {
		m.Version = r.nextVersionLocked(m.TenantID, m.Name)
	}
	r.models[m.ID] = *m
	return nil
}

// nextVersionLocked returns max(version)+1 for (tenantID, name) among the
// in-memory models. Callers must hold r.mu (read or write).
func (r *MemoryRegistry) nextVersionLocked(tenantID, name string) int {
	maxV := 0
	for _, existing := range r.models {
		if existing.TenantID == tenantID && existing.Name == name && existing.Version > maxV {
			maxV = existing.Version
		}
	}
	return maxV + 1
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
		if matchesFilter(m, f) {
			out = append(out, m)
		}
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return modelSortsBefore(out[i], out[j]) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// matchesFilter reports whether m satisfies every non-empty field of f.
func matchesFilter(m Model, f ListFilter) bool {
	if f.TenantID != "" && m.TenantID != f.TenantID {
		return false
	}
	if f.Name != "" && m.Name != f.Name {
		return false
	}
	if f.TaskKind != "" && m.TaskKind != f.TaskKind {
		return false
	}
	return true
}

// modelSortsBefore orders models by CreatedAt desc, then Version desc —
// the ordering [MemoryRegistry.List] promises (mirroring the SQL
// ORDER BY in PGRegistry.List).
func modelSortsBefore(a, b Model) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.Version > b.Version
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
