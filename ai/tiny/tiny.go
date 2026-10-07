// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tiny is a framework for training and serving small
// task-specific models: image classifiers and tiny generative LMs
// (for example, Gemma 3 270M/1B and Gemma 3n E2B) fine-tuned on one job.
//
// Go orchestrates; Python does the heavy lifting. Each [Trainer]
// implementation spawns a container (docker/k8s Job) with the right
// Python toolchain pinned — KerasHub + LoRA for Gemma, TensorFlow/Keras
// MobileNet V2 for LiteRT image classifiers — and captures the trained artifact
// into a [Registry] for later serving via [Predictor].
//
// The umbrella interfaces live here; concrete trainers are in
// sibling packages:
//
//   - ai/tiny/gemma/   — Gemma LoRA fine-tuning (text generation)
//   - ai/tiny/litert/  — Keras MobileNet V2 image classification
//   - ai/tiny/serve/   — inference adapters (ollama for Gemma, tflite for LiteRT)
package tiny

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/ai/tiny/internal/trainerio"
	"github.com/golusoris/golusoris/core/id"
	"github.com/golusoris/golusoris/core/validate"
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
	FormatTFLite    Format = "tflite"     // LiteRT FlatBuffer
	FormatKerasLoRA Format = "keras-lora" // KerasHub LoRA weights
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
	BaseModel string // Trainer-specific ("gemma3:270m", "keras:image/mobilenet-v2")
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

// ValidateClassifierLabels requires the stable external label order needed to
// map classifier output indexes to names.
func ValidateClassifierLabels(labels []string) error {
	if len(labels) < 2 {
		return errors.New("ai/tiny: classifier requires at least two labels")
	}
	seen := make(map[string]struct{}, len(labels))
	for index, label := range labels {
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("ai/tiny: classifier label %d must be non-empty", index)
		}
		if _, exists := seen[label]; exists {
			return fmt.Errorf("ai/tiny: classifier label %d must be unique", index)
		}
		seen[label] = struct{}{}
	}
	return nil
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
	if err := validateUTF8Slice("model.Labels", m.Labels); err != nil {
		return err
	}
	if err := validateFloatMap("model.Metrics", m.Metrics); err != nil {
		return err
	}
	if err := validateUTF8StringMap("model.Metadata", m.Metadata); err != nil {
		return err
	}
	return validateModelJSONSize(m)
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
	SaveJob(ctx context.Context, j *Job) error
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
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return &MemoryRegistry{
		jobs:   make(map[string]Job),
		models: make(map[string]Model),
		idGen:  id.New(),
		clk:    clk,
	}
}

// SaveJob stores a snapshot of j, assigning ID + CreatedAt when unset.
func (r *MemoryRegistry) SaveJob(_ context.Context, j *Job) error {
	if j == nil {
		return errors.New("ai/tiny: nil job")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.jobs[j.ID]
	if j.ID == "" {
		u, err := r.idGen.NewUUID()
		if err != nil {
			return fmt.Errorf("ai/tiny: job id: %w", err)
		}
		j.ID = u.String()
		existing, exists = Job{}, false
	}
	if exists {
		j.CreatedAt = existing.CreatedAt
	} else if j.CreatedAt.IsZero() {
		j.CreatedAt = r.clk.Now().UTC()
	}
	if err := ValidateJob(*j); err != nil {
		return err
	}
	if err := validateJobMetadata(*j); err != nil {
		return err
	}
	snapshot, err := cloneJob(*j)
	if err != nil {
		return err
	}
	r.jobs[j.ID] = snapshot
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
	return cloneJob(j)
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
	r.models[m.ID] = cloneModel(*m)
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
			return cloneModel(m), nil
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
	return cloneModel(best), nil
}

// List returns models matching f, sorted by (CreatedAt desc, Version desc).
func (r *MemoryRegistry) List(_ context.Context, f ListFilter) ([]Model, error) {
	r.mu.RLock()
	out := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		if matchesFilter(m, f) {
			out = append(out, cloneModel(m))
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
	if err := trainerio.ValidateStorageSegment("job.ID", j.ID); err != nil {
		return fmt.Errorf("ai/tiny: validate job ID: %w", err)
	}
	if err := trainerio.ValidateStorageSegment("job.Name", j.Name); err != nil {
		return fmt.Errorf("ai/tiny: validate job name: %w", err)
	}
	if j.TenantID != "" {
		if err := trainerio.ValidateStorageSegment("job.TenantID", j.TenantID); err != nil {
			return fmt.Errorf("ai/tiny: validate job tenant: %w", err)
		}
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

func cloneJob(job Job) (Job, error) {
	schemaHint, err := cloneJSONMap(job.Dataset.SchemaHint)
	if err != nil {
		return Job{}, fmt.Errorf("ai/tiny: clone job schema hint: %w", err)
	}
	hyperparams, err := cloneJSONMap(job.Hyperparams)
	if err != nil {
		return Job{}, fmt.Errorf("ai/tiny: clone job hyperparameters: %w", err)
	}
	job.Dataset.SchemaHint = schemaHint
	job.Hyperparams = hyperparams
	job.Tags = maps.Clone(job.Tags)
	return job, nil
}

func cloneJSONMap(value map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical JSON map: %w", err)
	}
	var clone map[string]any
	if err = json.Unmarshal(encoded, &clone); err != nil {
		return nil, fmt.Errorf("unmarshal canonical JSON map: %w", err)
	}
	return clone, nil
}

func validateJobMetadata(job Job) error {
	if err := validateJSONMap("job.Dataset.SchemaHint", job.Dataset.SchemaHint); err != nil {
		return err
	}
	if err := validateJSONMap("job.Hyperparams", job.Hyperparams); err != nil {
		return err
	}
	if err := validateUTF8StringMap("job.Tags", job.Tags); err != nil {
		return err
	}
	return validateJobMetadataSize(job)
}

const (
	maxRegistryJSONBytes = int(trainerio.DefaultMaxConfigBytes)
	maxJSONValues        = 100_000
	// Keep metadata below the pinned Python trainers' JSON recursion ceiling.
	maxJSONDepth = 512
)

type jsonValue struct {
	value any
	depth int
}

type jsonByteBudget struct {
	used       int
	limit      int
	values     int
	valueLimit int
	scope      string
}

func validateJobMetadataSize(job Job) error {
	budget := newJSONByteBudget("job serialized fields")
	serializedStrings := [...]string{
		job.ID,
		job.Name,
		job.TenantID,
		job.BaseModel,
		job.Dataset.ID,
		job.Dataset.URI,
		job.Dataset.Format,
		string(job.Dataset.Modality),
		string(job.Dataset.TaskKind),
	}
	for _, value := range serializedStrings {
		if err := budget.addString(value); err != nil {
			return err
		}
	}
	if err := budget.addJSONMap(job.Dataset.SchemaHint); err != nil {
		return err
	}
	if err := budget.addJSONMap(job.Hyperparams); err != nil {
		return err
	}
	return budget.addStringMap(job.Tags)
}

func validateModelJSONSize(model *Model) error {
	budget := newJSONByteBudget("model JSON fields")
	if err := budget.addStringSlice(orEmptySlice(model.Labels)); err != nil {
		return err
	}
	if err := budget.addFloatMap(orEmptyMetrics(model.Metrics)); err != nil {
		return err
	}
	return budget.addStringMap(orEmptyMetadata(model.Metadata))
}

func newJSONByteBudget(scope string) jsonByteBudget {
	return jsonByteBudget{
		limit: maxRegistryJSONBytes, valueLimit: maxJSONValues, scope: scope,
	}
}

func (b *jsonByteBudget) addJSONMap(value map[string]any) error {
	if value == nil {
		return b.add(4)
	}
	pending := make([]any, 0, len(value))
	if err := b.addMap(value, &pending); err != nil {
		return err
	}
	for index := range maxJSONValues {
		if index >= len(pending) {
			return nil
		}
		if err := b.addJSONValue(pending[index], &pending); err != nil {
			return err
		}
	}
	if len(pending) <= maxJSONValues {
		return nil
	}
	return errors.New("ai/tiny: metadata sizing exceeded nested value limit")
}

func (b *jsonByteBudget) addJSONValue(value any, pending *[]any) error {
	switch nested := value.(type) {
	case nil:
		return b.add(4)
	case bool:
		if nested {
			return b.add(4)
		}
		return b.add(5)
	case float64:
		return b.addFloat(nested)
	case string:
		return b.addString(nested)
	case []any:
		return b.addArray(nested, pending)
	case map[string]any:
		return b.addMap(nested, pending)
	default:
		return fmt.Errorf("ai/tiny: cannot size non-canonical JSON type %T", nested)
	}
}

func (b *jsonByteBudget) addArray(value []any, pending *[]any) error {
	if value == nil {
		return b.add(4)
	}
	if err := b.addValues(len(value)); err != nil {
		return err
	}
	if err := b.addContainer(len(value)); err != nil {
		return err
	}
	*pending = append(*pending, value...)
	return nil
}

func (b *jsonByteBudget) addMap(value map[string]any, pending *[]any) error {
	if value == nil {
		return b.add(4)
	}
	if err := b.addValues(len(value)); err != nil {
		return err
	}
	if err := b.addContainer(len(value)); err != nil {
		return err
	}
	for key, nested := range value {
		if err := b.addString(key); err != nil {
			return err
		}
		if err := b.add(1); err != nil {
			return err
		}
		*pending = append(*pending, nested)
	}
	return nil
}

func (b *jsonByteBudget) addStringMap(value map[string]string) error {
	if value == nil {
		return b.add(4)
	}
	if err := b.addValues(len(value)); err != nil {
		return err
	}
	if err := b.addContainer(len(value)); err != nil {
		return err
	}
	for key, nested := range value {
		if err := b.addString(key); err != nil {
			return err
		}
		if err := b.add(1); err != nil {
			return err
		}
		if err := b.addString(nested); err != nil {
			return err
		}
	}
	return nil
}

func (b *jsonByteBudget) addStringSlice(value []string) error {
	if value == nil {
		return b.add(4)
	}
	if err := b.addValues(len(value)); err != nil {
		return err
	}
	if err := b.addContainer(len(value)); err != nil {
		return err
	}
	for _, nested := range value {
		if err := b.addString(nested); err != nil {
			return err
		}
	}
	return nil
}

func (b *jsonByteBudget) addFloatMap(value map[string]float64) error {
	if value == nil {
		return b.add(4)
	}
	if err := b.addValues(len(value)); err != nil {
		return err
	}
	if err := b.addContainer(len(value)); err != nil {
		return err
	}
	for key, nested := range value {
		if err := b.addString(key); err != nil {
			return err
		}
		if err := b.add(1); err != nil {
			return err
		}
		if err := b.addFloat(nested); err != nil {
			return err
		}
	}
	return nil
}

func (b *jsonByteBudget) addContainer(values int) error {
	if err := b.add(2); err != nil {
		return err
	}
	if values > 1 {
		return b.add(values - 1)
	}
	return nil
}

func (b *jsonByteBudget) addFloat(value float64) error {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return errors.New("ai/tiny: metadata contains non-finite float64")
	}
	format := jsonFloatFormat(value)
	var encoded [32]byte
	result := strconv.AppendFloat(encoded[:0], value, format, -1, 64)
	length := len(result)
	if format == 'e' && hasPaddedNegativeExponent(result) {
		length--
	}
	return b.add(length)
}

func jsonFloatFormat(value float64) byte {
	abs := math.Abs(value)
	if abs == 0 {
		return 'f'
	}
	if abs < 1e-6 {
		return 'e'
	}
	if abs >= 1e21 {
		return 'e'
	}
	return 'f'
}

func hasPaddedNegativeExponent(encoded []byte) bool {
	length := len(encoded)
	if length < 4 {
		return false
	}
	return encoded[length-4] == 'e' && encoded[length-3] == '-' && encoded[length-2] == '0'
}

func (b *jsonByteBudget) addString(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("ai/tiny: metadata contains invalid UTF-8")
	}
	if err := b.add(len(value)); err != nil {
		return err
	}
	if err := b.add(2); err != nil {
		return err
	}
	for index := 0; index < len(value); {
		extra, width := jsonStringEscapeBytes(value[index:])
		if err := b.add(extra); err != nil {
			return err
		}
		index += width
	}
	return nil
}

func jsonStringEscapeBytes(value string) (extra, width int) {
	char := value[0]
	if char < utf8.RuneSelf {
		switch char {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			return 1, 1
		case '<', '>', '&':
			return 5, 1
		default:
			if char < ' ' {
				return 5, 1
			}
			return 0, 1
		}
	}
	runeValue, width := utf8.DecodeRuneInString(value)
	if runeValue == utf8.RuneError && width == 1 {
		return 5, 1
	}
	if runeValue == '\u2028' || runeValue == '\u2029' {
		return 3, width
	}
	return 0, width
}

func (b *jsonByteBudget) add(count int) error {
	if count > b.limit-b.used {
		return fmt.Errorf("ai/tiny: %s exceed %d encoded bytes", b.scope, b.limit)
	}
	b.used += count
	return nil
}

func (b *jsonByteBudget) addValues(count int) error {
	if count > b.valueLimit-b.values {
		return fmt.Errorf("ai/tiny: %s exceed %d values", b.scope, b.valueLimit)
	}
	b.values += count
	return nil
}

func validateJSONMap(name string, value map[string]any) error {
	pending, err := appendJSONChildren(name, nil, jsonValue{value: value, depth: 1}, maxJSONValues)
	if err != nil {
		return err
	}
	for index := 0; index < len(pending); index++ {
		if index >= maxJSONValues {
			return fmt.Errorf("ai/tiny: %s exceeds %d nested values", name, maxJSONValues)
		}
		pending, err = appendJSONChildren(name, pending, pending[index], maxJSONValues)
		if err != nil {
			return err
		}
	}
	return nil
}

func appendJSONChildren(name string, pending []jsonValue, node jsonValue, maxValues int) ([]jsonValue, error) {
	switch nested := node.value.(type) {
	case nil, bool:
		return pending, nil
	case float64:
		return validateJSONFloat(name, pending, nested)
	case string:
		return validateJSONString(name, pending, nested)
	case []any:
		return appendJSONArrayChildren(name, pending, nested, node.depth, maxValues)
	case map[string]any:
		return appendJSONObjectChildren(name, pending, nested, node.depth, maxValues)
	default:
		return nil, fmt.Errorf(
			"ai/tiny: %s contains non-canonical JSON type %T; use nil, bool, float64, string, []any, or map[string]any",
			name, nested,
		)
	}
}

func validateJSONFloat(name string, pending []jsonValue, value float64) ([]jsonValue, error) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, fmt.Errorf("ai/tiny: %s contains non-finite float64", name)
	}
	return pending, nil
}

func validateJSONString(name string, pending []jsonValue, value string) ([]jsonValue, error) {
	if !utf8.ValidString(value) {
		return nil, fmt.Errorf("ai/tiny: %s contains invalid UTF-8 string", name)
	}
	return pending, nil
}

func appendJSONArrayChildren(
	name string, pending []jsonValue, children []any, depth, maxValues int,
) ([]jsonValue, error) {
	if err := validateJSONContainer(name, len(pending), len(children), depth, maxValues); err != nil {
		return nil, err
	}
	for _, child := range children {
		pending = append(pending, jsonValue{value: child, depth: depth + 1})
	}
	return pending, nil
}

func appendJSONObjectChildren(
	name string, pending []jsonValue, children map[string]any, depth, maxValues int,
) ([]jsonValue, error) {
	if err := validateJSONContainer(name, len(pending), len(children), depth, maxValues); err != nil {
		return nil, err
	}
	for key, child := range children {
		if !utf8.ValidString(key) {
			return nil, fmt.Errorf("ai/tiny: %s contains invalid UTF-8 map key", name)
		}
		pending = append(pending, jsonValue{value: child, depth: depth + 1})
	}
	return pending, nil
}

func validateJSONContainer(name string, queued, children, depth, maxValues int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("ai/tiny: %s exceeds nesting depth %d", name, maxJSONDepth)
	}
	if children > maxValues-queued {
		return fmt.Errorf("ai/tiny: %s exceeds %d nested values", name, maxValues)
	}
	return nil
}

func validateUTF8Slice(name string, values []string) error {
	for index, value := range values {
		if !utf8.ValidString(value) {
			return fmt.Errorf("ai/tiny: %s[%d] contains invalid UTF-8", name, index)
		}
	}
	return nil
}

func validateFloatMap(name string, values map[string]float64) error {
	for key, value := range values {
		if !utf8.ValidString(key) {
			return fmt.Errorf("ai/tiny: %s contains invalid UTF-8 map key", name)
		}
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return fmt.Errorf("ai/tiny: %s[%q] contains non-finite float64", name, key)
		}
	}
	return nil
}

func validateUTF8StringMap(name string, values map[string]string) error {
	for key, value := range values {
		if !utf8.ValidString(key) {
			return fmt.Errorf("ai/tiny: %s contains invalid UTF-8 map key", name)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("ai/tiny: %s[%q] contains invalid UTF-8 string", name, key)
		}
	}
	return nil
}

func cloneModel(model Model) Model {
	model.Labels = slices.Clone(model.Labels)
	model.Metrics = maps.Clone(model.Metrics)
	model.Metadata = maps.Clone(model.Metadata)
	return model
}
