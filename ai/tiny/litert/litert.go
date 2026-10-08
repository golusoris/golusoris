// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package litert implements a [tiny.Trainer] for TensorFlow/Keras image
// classifiers. Go orchestrates; a pinned Python container trains a
// MobileNet V2 model and exports a LiteRT (`.tflite`) artifact.
//
// Supported tasks (driven by [tiny.Job.Dataset.TaskKind] +
// [tiny.Job.Dataset.Modality]):
//
//   - image / classify — Keras MobileNet V2
//
// Image datasets are safe tar/tar.gz/zip archives containing one directory
// per label.
//
// Output: `model.tflite` plus `metrics.json`. Both are uploaded to
// the configured [storage.Bucket].
package litert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
const DefaultImage = "ghcr.io/golusoris/tiny-litert-trainer:v1"

const (
	// BaseTextAverageWordEmbedding names the removed text classifier.
	//
	// Deprecated: LiteRT training supports image classification only.
	BaseTextAverageWordEmbedding = "tensorflow:text/average-word-embedding"
	// LegacyBaseTextAverageWordEmbedding preserves the former Model Maker identifier.
	//
	// Deprecated: LiteRT training supports image classification only.
	LegacyBaseTextAverageWordEmbedding = "mediapipe:text/average-word-embedding"
	// BaseTextMobileBERT names the removed Model Maker MobileBERT trainer.
	//
	// Deprecated: no supported Keras 3 replacement exists.
	BaseTextMobileBERT = "mediapipe:text/mobilebert"
	// BaseImageMobileNetV2 selects the Keras MobileNet V2 backbone.
	BaseImageMobileNetV2 = "keras:image/mobilenet-v2"
	// LegacyBaseImageMobileNetV2 preserves the former Model Maker identifier.
	//
	// Deprecated: use [BaseImageMobileNetV2].
	LegacyBaseImageMobileNetV2 = "mediapipe:image/mobilenet-v2"
	// BaseImageMobileNetV2Keras preserves the second former MobileNet V2 identifier.
	//
	// Deprecated: use [BaseImageMobileNetV2].
	BaseImageMobileNetV2Keras = "mediapipe:image/mobilenet-v2-keras"
	// BaseImageEfficientNetLite0 names the removed Model Maker EfficientNet-Lite0 trainer.
	//
	// Deprecated: Keras EfficientNetB0 is a different architecture.
	BaseImageEfficientNetLite0 = "mediapipe:image/efficientnet-lite0"
	// BaseImageEfficientNetLite2 names the removed Model Maker EfficientNet-Lite2 trainer.
	//
	// Deprecated: Keras EfficientNetB2 is a different architecture.
	BaseImageEfficientNetLite2 = "mediapipe:image/efficientnet-lite2"
	// BaseImageEfficientNetLite4 names the removed Model Maker EfficientNet-Lite4 trainer.
	//
	// Deprecated: Keras EfficientNetB4 is a different architecture.
	BaseImageEfficientNetLite4 = "mediapipe:image/efficientnet-lite4"
	// DefaultMaxLogBytes caps retained container output at 1 MiB.
	DefaultMaxLogBytes = trainerio.DefaultMaxLogBytes
	// DefaultMaxArtifactBytes caps a LiteRT artifact at 1 GiB.
	DefaultMaxArtifactBytes = trainerio.DefaultMaxArtifactBytes
	// DefaultMaxMetricsBytes caps the required metrics sidecar at 1 MiB.
	DefaultMaxMetricsBytes = trainerio.DefaultMaxMetricsBytes
	// DefaultMaxDatasetBytes caps a staged or fetched dataset at 10 GiB.
	DefaultMaxDatasetBytes       = trainerio.DefaultMaxDatasetBytes
	litertWorkspaceOverheadBytes = tiny.DefaultTmpfsBytes
)

var supportedBaseModels = map[string]tiny.Modality{
	BaseImageMobileNetV2: tiny.ModalityImage,
}

// ArtifactName is the filename the trainer container writes into
// /work/output.
const ArtifactName = "model.tflite"

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
	// (`<prefix>/<name>/<job_id>/model.tflite`). Default: "models/litert".
	KeyPrefix string
	// Image is a digest-pinned trainer image. Required.
	Image string
	// AllowUnpinnedImage explicitly trusts a mutable Image for local
	// development. Production callers must leave it false.
	AllowUnpinnedImage bool
	// GPUs passed to the Runner (0 = CPU).
	GPUs int
	// Timeout caps a single training run. Zero uses [tiny.DefaultRunTimeout].
	Timeout time.Duration
	// ExtraEnv is merged into the Runner env. Trainer-managed keys
	// (TINY_*) are not overridable.
	ExtraEnv map[string]string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// MaxLogBytes caps retained stdout plus stderr. Zero uses 1 MiB.
	MaxLogBytes int64
	// MaxArtifactBytes caps model.tflite. Zero uses 1 GiB.
	MaxArtifactBytes int64
	// MaxMetricsBytes caps metrics.json. Zero uses 1 MiB.
	MaxMetricsBytes int64
	// MaxDatasetBytes caps a local staged file. Zero uses 10 GiB.
	MaxDatasetBytes int64
}

// Trainer is a [tiny.Trainer] that trains a TensorFlow/Keras classifier
// and emits a LiteRT `.tflite` artifact.
type Trainer struct {
	opts Options
}

// NewTrainer validates opts and returns a Trainer.
func NewTrainer(opts Options) (*Trainer, error) {
	trainer, err := trainerio.ConstructTrainer(opts, normalizeOptions, func(normalized Options) *Trainer {
		return &Trainer{opts: normalized}
	})
	if err != nil {
		return nil, fmt.Errorf("ai/tiny/litert: construct trainer: %w", err)
	}
	return trainer, nil
}

func validateOptions(opts *Options) error {
	if validate.IsNil(opts.Runner) {
		return errors.New("ai/tiny/litert: Runner required")
	}
	if validate.IsNil(opts.Bucket) {
		return errors.New("ai/tiny/litert: Bucket required")
	}
	if opts.Image == "" {
		return errors.New("ai/tiny/litert: Image required; no trainer image is currently published")
	}
	if opts.GPUs < 0 {
		return errors.New("ai/tiny/litert: GPUs must not be negative")
	}
	if err := tiny.ValidateImageReference(opts.Image, opts.AllowUnpinnedImage); err != nil {
		return fmt.Errorf("ai/tiny/litert: validate Image: %w", err)
	}
	datasetRoot, err := trainerio.CanonicalDatasetRoot(opts.DatasetRoot)
	if err != nil {
		return fmt.Errorf("ai/tiny/litert: validate DatasetRoot: %w", err)
	}
	opts.DatasetRoot = datasetRoot
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "models/litert"
	}
	if err = trainerio.ValidateKeyPrefix(opts.KeyPrefix); err != nil {
		return fmt.Errorf("ai/tiny/litert: validate KeyPrefix: %w", err)
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
		return Options{}, fmt.Errorf("ai/tiny/litert: %w", err)
	}
	limits, err := trainerio.NormalizeLimits(trainerio.Limits{
		Log: opts.MaxLogBytes, Artifact: opts.MaxArtifactBytes, Metrics: opts.MaxMetricsBytes,
	}, "litert")
	if err != nil {
		return Options{}, fmt.Errorf("ai/tiny/litert: normalize output limits: %w", err)
	}
	opts.MaxLogBytes = limits.Log
	opts.MaxArtifactBytes = limits.Artifact
	opts.MaxMetricsBytes = limits.Metrics
	opts.MaxDatasetBytes, err = trainerio.NormalizeLimit(
		opts.MaxDatasetBytes, DefaultMaxDatasetBytes, "litert MaxDatasetBytes",
	)
	if err != nil {
		return Options{}, fmt.Errorf("ai/tiny/litert: normalize dataset limit: %w", err)
	}
	if opts.MaxDatasetBytes > DefaultMaxDatasetBytes {
		return Options{}, fmt.Errorf(
			"ai/tiny/litert: MaxDatasetBytes exceeds workspace-safe limit %d",
			DefaultMaxDatasetBytes,
		)
	}
	opts.ExtraEnv = maps.Clone(opts.ExtraEnv)
	return opts, nil
}

// Name reports "litert".
func (*Trainer) Name() string { return "litert" }

// Train stages inputs, runs the TensorFlow container, and uploads
// the `.tflite` artifact. Version is left zero — the Registry assigns
// it on SaveModel.
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

	metrics, labels, err := t.readSidecar(ctx, outputDir)
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
		return tiny.Model{}, fmt.Errorf("ai/tiny/litert: read artifact: %w", err)
	}

	return tiny.Model{
		JobID:     job.ID,
		Name:      job.Name,
		TenantID:  job.TenantID,
		URI:       obj.Key,
		Format:    tiny.FormatTFLite,
		Modality:  job.Dataset.Modality,
		TaskKind:  job.Dataset.TaskKind,
		BaseModel: job.BaseModel,
		Labels:    labels,
		Metrics:   metrics,
		Metadata:  maps.Clone(job.Tags),
	}, nil
}

// validateJob rejects jobs this trainer cannot serve.
func validateJob(job tiny.Job) error {
	if err := tiny.ValidateJob(job); err != nil {
		return fmt.Errorf("ai/tiny/litert: validate: %w", err)
	}
	if job.Dataset.TaskKind != tiny.TaskClassify {
		return fmt.Errorf("ai/tiny/litert: need TaskKind=classify, got %s", job.Dataset.TaskKind)
	}
	modality, supported := supportedBaseModels[job.BaseModel]
	if !supported {
		return fmt.Errorf("ai/tiny/litert: BaseModel %q not supported", job.BaseModel)
	}
	if job.Dataset.Modality != modality {
		return fmt.Errorf(
			"ai/tiny/litert: BaseModel %q requires Modality=%s, got %s",
			job.BaseModel, modality, job.Dataset.Modality,
		)
	}
	if job.Dataset.Format != "tar" && job.Dataset.Format != "tar.gz" &&
		job.Dataset.Format != "tgz" && job.Dataset.Format != "zip" {
		return fmt.Errorf(
			"ai/tiny/litert: image dataset format %q not supported (want tar, tar.gz, tgz, or zip)",
			job.Dataset.Format,
		)
	}
	return nil
}

// buildEnv merges ExtraEnv under the trainer-managed keys.
func (t *Trainer) buildEnv(job tiny.Job) map[string]string {
	env := map[string]string{
		"TINY_JOB_NAME": job.Name,
		"TINY_MODALITY": string(job.Dataset.Modality),
		"TINY_TASK":     string(job.Dataset.TaskKind),
	}
	for k, v := range t.opts.ExtraEnv {
		if _, reserved := env[k]; !reserved {
			env[k] = v
		}
	}
	return env
}

// runContainer invokes the Runner and drains its output into structured
// logs whether or not Run errored.
func (t *Trainer) runContainer(ctx context.Context, job tiny.Job, inputDir, outputDir string) error {
	runCtx, cancel := context.WithTimeout(ctx, t.opts.Timeout)
	defer cancel()
	t.opts.Logger.InfoContext(
		runCtx, "ai/tiny/litert: training start",
		slog.String("job", job.Name),
		slog.String("modality", string(job.Dataset.Modality)),
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
		NetworkDisabled:    true,
		MaxOutputFileBytes: max(t.opts.MaxArtifactBytes, t.opts.MaxMetricsBytes),
		TmpfsBytes:         t.opts.MaxDatasetBytes + litertWorkspaceOverheadBytes,
		AllowUnpinnedImage: t.opts.AllowUnpinnedImage,
	})
	trainerio.DrainLog(runCtx, t.opts.Logger, "ai/tiny/litert: trainer", logBuf)
	if runErr != nil {
		return fmt.Errorf("ai/tiny/litert: runner: %w", runErr)
	}
	return nil
}

// readSidecar parses the required metrics sidecar and validates the
// classifier label order before the artifact can be published.
func (t *Trainer) readSidecar(
	ctx context.Context, outputDir string,
) (map[string]float64, []string, error) {
	metrics := map[string]float64{}
	metricsBytes, mErr := trainerio.ReadOutput(ctx, outputDir, MetricsName, t.opts.MaxMetricsBytes)
	if mErr != nil {
		return nil, nil, fmt.Errorf("ai/tiny/litert: read %s: %w", MetricsName, mErr)
	}
	var sidecar struct {
		Metrics map[string]float64 `json:"metrics"`
		Labels  []string           `json:"labels"`
	}
	if jerr := json.Unmarshal(metricsBytes, &sidecar); jerr != nil {
		return nil, nil, fmt.Errorf("ai/tiny/litert: parse metrics: %w", jerr)
	}
	if err := tiny.ValidateClassifierLabels(sidecar.Labels); err != nil {
		return nil, nil, fmt.Errorf("ai/tiny/litert: validate metrics labels: %w", err)
	}
	if sidecar.Metrics != nil {
		metrics = sidecar.Metrics
	}
	return metrics, append([]string(nil), sidecar.Labels...), nil
}

// cleanup removes the staged work dir; a failure is logged, not returned.
func (t *Trainer) cleanup(ctx context.Context, workDir string) {
	if rmErr := trainerio.Cleanup(workDir); rmErr != nil {
		t.opts.Logger.WarnContext(ctx, "ai/tiny/litert: remove work dir", slog.String("error", rmErr.Error()))
	}
}

// stageWorkDir creates a tmp work dir with input/ + output/ subdirs
// and writes config.json describing the job for the container.
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
		ctx, "tiny-litert-*", cfg, datasetRoot, job.TenantID, job.Dataset.URI, maxDatasetBytes,
	)
	if err != nil {
		return "", "", "", fmt.Errorf("ai/tiny/litert: stage: %w", err)
	}
	return workDir, inputDir, outputDir, nil
}
