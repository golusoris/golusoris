// Package litert implements a [tiny.Trainer] for MediaPipe Model
// Maker. Go orchestrates; a pinned Python container trains a
// task-specific model and exports a LiteRT (`.tflite`) artifact for
// on-device inference.
//
// Supported tasks (driven by [tiny.Job.Dataset.TaskKind] +
// [tiny.Job.Dataset.Modality]):
//
//   - text / classify  — MediaPipe text_classifier (MobileBERT backbone)
//   - image / classify — MediaPipe image_classifier (EfficientNet-Lite backbone)
//   - audio / classify — MediaPipe audio_classifier (YAMNet backbone)
//
// Dataset layout is task-dependent; the Job's `Dataset.URI` points
// at a tarball the container expands. See MediaPipe's Model Maker
// docs for the expected directory structure per task.
//
// Output: `model.tflite` plus `metrics.json`. Both are uploaded to
// the configured [storage.Bucket].
package litert

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/storage"
)

// DefaultImage is the published training image.
const DefaultImage = "ghcr.io/golusoris/tiny-litert-trainer:v1"

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
	// KeyPrefix is prepended to the artifact key
	// (`<prefix>/<name>/<job_id>/model.tflite`). Default: "models/litert".
	KeyPrefix string
	// Image overrides [DefaultImage].
	Image string
	// GPUs passed to the Runner (0 = CPU). CPU is usually enough for
	// MobileBERT/EfficientNet-Lite/YAMNet transfer learning.
	GPUs int
	// Timeout caps a single training run. 0 = no cap beyond ctx.
	Timeout time.Duration
	// ExtraEnv is merged into the Runner env. Trainer-managed keys
	// (TINY_*) are not overridable.
	ExtraEnv map[string]string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Trainer is a [tiny.Trainer] that fine-tunes a MediaPipe Model Maker
// task and emits a LiteRT `.tflite` artifact.
type Trainer struct {
	opts Options
}

// NewTrainer validates opts and returns a Trainer.
func NewTrainer(opts Options) (*Trainer, error) {
	if opts.Runner == nil {
		return nil, errors.New("ai/tiny/litert: Runner required")
	}
	if opts.Bucket == nil {
		return nil, errors.New("ai/tiny/litert: Bucket required")
	}
	if opts.Image == "" {
		opts.Image = DefaultImage
	}
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "models/litert"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Trainer{opts: opts}, nil
}

// Name reports "litert".
func (*Trainer) Name() string { return "litert" }

// Train stages inputs, runs the Model Maker container, and uploads
// the `.tflite` artifact. Version is left zero — the Registry assigns
// it on SaveModel.
func (t *Trainer) Train(ctx context.Context, job tiny.Job) (tiny.Model, error) {
	if err := tiny.ValidateJob(job); err != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/litert: validate: %w", err)
	}
	if job.Dataset.TaskKind != tiny.TaskClassify {
		return tiny.Model{}, fmt.Errorf("ai/tiny/litert: need TaskKind=classify, got %s", job.Dataset.TaskKind)
	}
	switch job.Dataset.Modality {
	case tiny.ModalityText, tiny.ModalityImage, tiny.ModalityAudio:
	default:
		return tiny.Model{}, fmt.Errorf("ai/tiny/litert: Modality %s not supported (want text|image|audio)", job.Dataset.Modality)
	}

	workDir, inputDir, outputDir, err := stageWorkDir(job)
	if err != nil {
		return tiny.Model{}, err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

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

	t.opts.Logger.InfoContext(ctx, "ai/tiny/litert: training start",
		slog.String("job", job.Name),
		slog.String("modality", string(job.Dataset.Modality)),
		slog.String("runner", t.opts.Runner.Name()),
	)
	var logBuf bytes.Buffer
	runErr := t.opts.Runner.Run(ctx, tiny.RunSpec{
		Image:     t.opts.Image,
		Env:       env,
		InputDir:  inputDir,
		OutputDir: outputDir,
		Timeout:   t.opts.Timeout,
		GPUs:      t.opts.GPUs,
		Logger:    &logBuf,
	})
	sc := bufio.NewScanner(&logBuf)
	for sc.Scan() {
		t.opts.Logger.InfoContext(ctx, "ai/tiny/litert: trainer", slog.String("line", sc.Text()))
	}
	if runErr != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/litert: runner: %w", runErr)
	}

	artifactBytes, readErr := os.ReadFile(filepath.Join(outputDir, ArtifactName))
	if readErr != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/litert: read artifact: %w", readErr)
	}
	metrics := map[string]float64{}
	labels := []string{}
	if metricsBytes, mErr := os.ReadFile(filepath.Join(outputDir, MetricsName)); mErr == nil {
		// Metrics file may carry both scalar metrics and a "labels"
		// array — decode into a loose map and sort them out.
		var sidecar struct {
			Metrics map[string]float64 `json:"metrics"`
			Labels  []string           `json:"labels"`
		}
		if jerr := json.Unmarshal(metricsBytes, &sidecar); jerr != nil {
			t.opts.Logger.WarnContext(ctx, "ai/tiny/litert: parse metrics", slog.String("error", jerr.Error()))
		} else {
			if sidecar.Metrics != nil {
				metrics = sidecar.Metrics
			}
			labels = sidecar.Labels
		}
	}

	key := filepath.ToSlash(filepath.Join(t.opts.KeyPrefix, job.Name, job.ID, ArtifactName))
	obj, err := t.opts.Bucket.Put(ctx, key, strings.NewReader(string(artifactBytes)), storage.PutOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/litert: upload: %w", err)
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
		Metadata:  copyStringMap(job.Tags),
	}, nil
}

// stageWorkDir creates a tmp work dir with input/ + output/ subdirs
// and writes config.json describing the job for the container.
func stageWorkDir(job tiny.Job) (workDir, inputDir, outputDir string, err error) {
	workDir, err = os.MkdirTemp("", "tiny-litert-*")
	if err != nil {
		return "", "", "", fmt.Errorf("ai/tiny/litert: mktemp: %w", err)
	}
	inputDir = filepath.Join(workDir, "input")
	outputDir = filepath.Join(workDir, "output")
	if mkErr := os.MkdirAll(inputDir, 0o750); mkErr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/litert: mkdir input: %w", mkErr)
	}
	if mkErr := os.MkdirAll(outputDir, 0o750); mkErr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/litert: mkdir output: %w", mkErr)
	}
	cfg := map[string]any{
		"job_id":      job.ID,
		"job_name":    job.Name,
		"tenant_id":   job.TenantID,
		"base_model":  job.BaseModel,
		"modality":    string(job.Dataset.Modality),
		"task_kind":   string(job.Dataset.TaskKind),
		"dataset_uri": job.Dataset.URI,
		"dataset_fmt": job.Dataset.Format,
		"hyperparams": job.Hyperparams,
		"tags":        job.Tags,
	}
	cfgBytes, jerr := json.Marshal(cfg)
	if jerr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/litert: marshal config: %w", jerr)
	}
	if writeErr := os.WriteFile(filepath.Join(inputDir, "config.json"), cfgBytes, 0o600); writeErr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/litert: write config: %w", writeErr)
	}
	return workDir, inputDir, outputDir, nil
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
