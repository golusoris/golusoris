// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package gemma implements a [tiny.Trainer] for LoRA fine-tuning
// Google Gemma 3 / Gemma 3n on a JSONL prompt/response corpus. Go
// orchestrates; a pinned Python container does the training via
// KerasHub.
//
// Dataset schema (JSONL):
//
//	{"prompt":"…", "response":"…"}
//	{"prompt":"…", "response":"…"}
//
// Supported base models: "gemma3:270m", "gemma3:1b", "gemma3:4b-text",
// "gemma3n:e2b", "gemma3n:e4b". The container resolves the base
// checkpoint at runtime; callers explicitly enable network access and supply
// credentials required by the selected preset through [Options.ExtraEnv].
//
// Output: KerasHub LoRA weights as a `.lora.h5` file plus a
// `metrics.json` sidecar. Both are uploaded to the configured
// [storage.Bucket].
package gemma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"time"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/internal/trainerio"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/storage"
)

// DefaultImage is the legacy mutable trainer image name.
//
// Deprecated: no trainer image is currently published at this reference.
// [NewTrainer] requires an explicit digest-pinned [Options.Image].
const DefaultImage = "ghcr.io/golusoris/tiny-gemma-trainer:v1"

const (
	// BaseGemma3270M is the KerasHub gemma3_270m preset.
	BaseGemma3270M = "gemma3:270m"
	// BaseGemma31B is the KerasHub gemma3_1b preset.
	BaseGemma31B = "gemma3:1b"
	// BaseGemma34BText is the KerasHub text-only gemma3_4b_text preset.
	BaseGemma34BText = "gemma3:4b-text"
	// BaseGemma3NE2B is the KerasHub gemma3n_e2b preset.
	BaseGemma3NE2B = "gemma3n:e2b"
	// BaseGemma3NE4B is the KerasHub gemma3n_e4b preset.
	BaseGemma3NE4B = "gemma3n:e4b"
	// DefaultMaxLogBytes caps retained container output at 1 MiB.
	DefaultMaxLogBytes = trainerio.DefaultMaxLogBytes
	// DefaultMaxArtifactBytes caps a LoRA artifact at 1 GiB.
	DefaultMaxArtifactBytes = trainerio.DefaultMaxArtifactBytes
	// DefaultMaxMetricsBytes caps the optional metrics sidecar at 1 MiB.
	DefaultMaxMetricsBytes = trainerio.DefaultMaxMetricsBytes
	// DefaultMaxDatasetBytes caps a staged or fetched dataset at 10 GiB.
	DefaultMaxDatasetBytes = trainerio.DefaultMaxDatasetBytes
)

var supportedBaseModels = map[string]struct{}{
	BaseGemma3270M: {}, BaseGemma31B: {}, BaseGemma34BText: {},
	BaseGemma3NE2B: {}, BaseGemma3NE4B: {},
}

// ArtifactName is the filename the trainer container writes into
// /work/output.
const ArtifactName = "adapter.lora.h5"

// MetricsName is the sidecar metrics file written alongside the
// artifact.
const MetricsName = "metrics.json"

// Options configures a [Trainer].
type Options struct {
	// Runner launches the training container. Required.
	Runner tiny.Runner
	// Bucket receives the trained artifact + metrics. Required.
	Bucket storage.Bucket
	// DatasetRoot is the absolute canonical root containing one directory per
	// tenant (`<root>/<tenant>` or `<root>/_default`). Required.
	DatasetRoot string
	// KeyPrefix is prepended to the artifact key
	// (`<prefix>/<name>/<job-id>/adapter.lora.h5`). Default: "models/gemma".
	KeyPrefix string
	// Image is a digest-pinned trainer image. Required.
	Image string
	// AllowUnpinnedImage explicitly trusts a mutable Image for local
	// development. Production callers must leave it false.
	AllowUnpinnedImage bool
	// AllowNetwork permits trainer-container egress for KerasHub preset
	// acquisition. It defaults to false and is required for an uncached preset.
	AllowNetwork bool
	// GPUs passed to the Runner (0 = CPU). Training on CPU is only
	// useful for smoke tests.
	GPUs int
	// Timeout caps a single training run. Zero uses [tiny.DefaultRunTimeout].
	Timeout time.Duration
	// ExtraEnv is merged into the Runner env (useful for KAGGLE_USERNAME,
	// KAGGLE_KEY, …). Trainer-managed keys (TINY_*) are
	// not overridable.
	ExtraEnv map[string]string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// MaxLogBytes caps retained stdout plus stderr. Zero uses 1 MiB.
	MaxLogBytes int64
	// MaxArtifactBytes caps adapter.lora.h5. Zero uses 1 GiB.
	MaxArtifactBytes int64
	// MaxMetricsBytes caps metrics.json. Zero uses 1 MiB.
	MaxMetricsBytes int64
	// MaxDatasetBytes caps a local staged file. Zero uses 10 GiB.
	MaxDatasetBytes int64
}

// Trainer is a [tiny.Trainer] that fine-tunes Gemma with LoRA.
type Trainer struct {
	opts Options
}

// NewTrainer validates opts and returns a Trainer.
func NewTrainer(opts Options) (*Trainer, error) {
	trainer, err := trainerio.ConstructTrainer(opts, normalizeOptions, func(normalized Options) *Trainer {
		return &Trainer{opts: normalized}
	})
	if err != nil {
		return nil, fmt.Errorf("ai/tiny/gemma: construct trainer: %w", err)
	}
	return trainer, nil
}

func validateOptions(opts *Options) error {
	if validate.IsNil(opts.Runner) {
		return errors.New("ai/tiny/gemma: Runner required")
	}
	if validate.IsNil(opts.Bucket) {
		return errors.New("ai/tiny/gemma: Bucket required")
	}
	if opts.Image == "" {
		return errors.New("ai/tiny/gemma: Image required; no trainer image is currently published")
	}
	if opts.GPUs < 0 {
		return errors.New("ai/tiny/gemma: GPUs must not be negative")
	}
	if err := tiny.ValidateImageReference(opts.Image, opts.AllowUnpinnedImage); err != nil {
		return fmt.Errorf("ai/tiny/gemma: validate Image: %w", err)
	}
	datasetRoot, err := trainerio.CanonicalDatasetRoot(opts.DatasetRoot)
	if err != nil {
		return fmt.Errorf("ai/tiny/gemma: validate DatasetRoot: %w", err)
	}
	opts.DatasetRoot = datasetRoot
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "models/gemma"
	}
	if err = trainerio.ValidateKeyPrefix(opts.KeyPrefix); err != nil {
		return fmt.Errorf("ai/tiny/gemma: validate KeyPrefix: %w", err)
	}
	return nil
}

func normalizeOptions(opts Options) (Options, error) {
	if err := validateOptions(&opts); err != nil {
		return Options{}, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	var err error
	opts.Timeout, err = tiny.NormalizeRunTimeout(opts.Timeout)
	if err != nil {
		return Options{}, fmt.Errorf("ai/tiny/gemma: %w", err)
	}
	limits, err := trainerio.NormalizeLimits(trainerio.Limits{
		Log: opts.MaxLogBytes, Artifact: opts.MaxArtifactBytes, Metrics: opts.MaxMetricsBytes,
	}, "gemma")
	if err != nil {
		return Options{}, fmt.Errorf("ai/tiny/gemma: normalize output limits: %w", err)
	}
	opts.MaxLogBytes = limits.Log
	opts.MaxArtifactBytes = limits.Artifact
	opts.MaxMetricsBytes = limits.Metrics
	opts.MaxDatasetBytes, err = trainerio.NormalizeLimit(
		opts.MaxDatasetBytes, DefaultMaxDatasetBytes, "gemma MaxDatasetBytes",
	)
	if err != nil {
		return Options{}, fmt.Errorf("ai/tiny/gemma: normalize dataset limit: %w", err)
	}
	opts.ExtraEnv = maps.Clone(opts.ExtraEnv)
	return opts, nil
}

// Name reports "gemma".
func (*Trainer) Name() string { return "gemma" }

// Train stages the dataset + hyperparams, invokes the Runner, and
// uploads the resulting LoRA bundle. The returned [tiny.Model] has a
// zero Version — the Registry assigns it on SaveModel.
func (t *Trainer) Train(ctx context.Context, job tiny.Job) (tiny.Model, error) {
	if err := validateJob(job); err != nil {
		return tiny.Model{}, err
	}

	workDir, inputDir, outputDir, err := stageWorkDir(
		ctx, job, t.opts.DatasetRoot, t.opts.MaxDatasetBytes,
	)
	if err != nil {
		return tiny.Model{}, err
	}
	defer t.cleanup(ctx, workDir)

	if runErr := t.runContainer(ctx, job, inputDir, outputDir); runErr != nil {
		return tiny.Model{}, runErr
	}

	metrics, err := t.readMetrics(ctx, outputDir)
	if err != nil {
		return tiny.Model{}, err
	}

	obj, err := trainerio.UploadOutput(
		ctx, t.opts.Bucket, trainerio.OutputUpload{
			OutputDir: outputDir, SnapshotDir: workDir, ArtifactName: ArtifactName,
			KeyPrefix: t.opts.KeyPrefix, TenantID: job.TenantID, ModelName: job.Name,
			JobID: job.ID, MaxBytes: t.opts.MaxArtifactBytes,
			ContentType: storage.DefaultContentType,
		},
	)
	if err != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/gemma: read artifact: %w", err)
	}

	return tiny.Model{
		JobID:     job.ID,
		Name:      job.Name,
		TenantID:  job.TenantID,
		URI:       obj.Key,
		Format:    tiny.FormatKerasLoRA,
		Modality:  tiny.ModalityText,
		TaskKind:  tiny.TaskGenerate,
		BaseModel: job.BaseModel,
		Metrics:   metrics,
		Metadata:  maps.Clone(job.Tags),
	}, nil
}

// validateJob rejects jobs this trainer cannot serve.
func validateJob(job tiny.Job) error {
	if err := tiny.ValidateJob(job); err != nil {
		return fmt.Errorf("ai/tiny/gemma: validate: %w", err)
	}
	if job.Dataset.Modality != tiny.ModalityText || job.Dataset.TaskKind != tiny.TaskGenerate {
		return fmt.Errorf("ai/tiny/gemma: need Modality=text + TaskKind=generate, got %s/%s",
			job.Dataset.Modality, job.Dataset.TaskKind)
	}
	if _, supported := supportedBaseModels[job.BaseModel]; !supported {
		return fmt.Errorf("ai/tiny/gemma: BaseModel %q not supported", job.BaseModel)
	}
	if job.Dataset.Format != "jsonl" {
		return fmt.Errorf("ai/tiny/gemma: dataset format %q not supported (want jsonl)", job.Dataset.Format)
	}
	return nil
}

// buildEnv merges ExtraEnv under the trainer-managed keys.
func (t *Trainer) buildEnv(job tiny.Job) map[string]string {
	env := map[string]string{
		"TINY_JOB_NAME":   job.Name,
		"TINY_BASE_MODEL": job.BaseModel,
	}
	for k, v := range t.opts.ExtraEnv {
		if _, reserved := env[k]; !reserved {
			env[k] = v
		}
	}
	return env
}

// runContainer invokes the Runner and drains its output into structured
// logs whether or not Run errored — failure output is often more useful
// than success output.
func (t *Trainer) runContainer(ctx context.Context, job tiny.Job, inputDir, outputDir string) error {
	runCtx, cancel := context.WithTimeout(ctx, t.opts.Timeout)
	defer cancel()
	t.opts.Logger.InfoContext(
		runCtx, "ai/tiny/gemma: training start",
		slog.String("job", job.Name),
		slog.String("base", job.BaseModel),
		slog.String("runner", t.opts.Runner.Name()),
	)
	logBuf := trainerio.NewCappedLog(t.opts.MaxLogBytes)
	runErr := t.opts.Runner.Run(runCtx, tiny.RunSpec{
		Image:              t.opts.Image,
		Env:                t.buildEnv(job),
		InputDir:           inputDir,
		OutputDir:          outputDir,
		Timeout:            t.opts.Timeout,
		GPUs:               t.opts.GPUs,
		Logger:             logBuf,
		NetworkDisabled:    !t.opts.AllowNetwork,
		MaxOutputFileBytes: max(t.opts.MaxArtifactBytes, t.opts.MaxMetricsBytes),
		AllowUnpinnedImage: t.opts.AllowUnpinnedImage,
	})
	trainerio.DrainLog(runCtx, t.opts.Logger, "ai/tiny/gemma: trainer", logBuf)
	if runErr != nil {
		return fmt.Errorf("ai/tiny/gemma: runner: %w", runErr)
	}
	return nil
}

// readMetrics parses the optional metrics sidecar; a missing or
// malformed file yields an empty map.
func (t *Trainer) readMetrics(ctx context.Context, outputDir string) (map[string]float64, error) {
	metrics := map[string]float64{}
	metricsBytes, mErr := trainerio.ReadOutput(ctx, outputDir, MetricsName, t.opts.MaxMetricsBytes)
	if mErr != nil {
		if errors.Is(mErr, fs.ErrNotExist) {
			return metrics, nil
		}
		return nil, fmt.Errorf("ai/tiny/gemma: read metrics: %w", mErr)
	}
	if jerr := json.Unmarshal(metricsBytes, &metrics); jerr != nil {
		t.opts.Logger.WarnContext(ctx, "ai/tiny/gemma: parse metrics", slog.String("error", jerr.Error()))
	}
	return metrics, nil
}

// cleanup removes the staged work dir; a failure is logged, not returned.
func (t *Trainer) cleanup(ctx context.Context, workDir string) {
	if rmErr := trainerio.Cleanup(workDir); rmErr != nil {
		t.opts.Logger.WarnContext(ctx, "ai/tiny/gemma: remove work dir", slog.String("error", rmErr.Error()))
	}
}

// stageWorkDir creates a temp work directory with input/ + output/
// subdirs and writes config.json into input/ for the container to read.
func stageWorkDir(
	ctx context.Context, job tiny.Job, datasetRoot string, maxDatasetBytes int64,
) (workDir, inputDir, outputDir string, err error) {
	cfg := map[string]any{
		"job_id":      job.ID,
		"job_name":    job.Name,
		"tenant_id":   job.TenantID,
		"base_model":  job.BaseModel,
		"modality":    string(job.Dataset.Modality),
		"task_kind":   string(job.Dataset.TaskKind),
		"dataset_uri": job.Dataset.URI,
		"dataset_fmt": job.Dataset.Format,
		"schema_hint": job.Dataset.SchemaHint,
		"hyperparams": job.Hyperparams,
		"tags":        job.Tags,
	}
	workDir, inputDir, outputDir, err = trainerio.StageDataset(
		ctx, "tiny-gemma-*", cfg, datasetRoot, job.TenantID, job.Dataset.URI, maxDatasetBytes,
	)
	if err != nil {
		return "", "", "", fmt.Errorf("ai/tiny/gemma: stage: %w", err)
	}
	return workDir, inputDir, outputDir, nil
}
