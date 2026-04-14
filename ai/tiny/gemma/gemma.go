// Package gemma implements a [tiny.Trainer] for LoRA fine-tuning
// Google Gemma 3 / Gemma 3n on a JSONL prompt/response corpus. Go
// orchestrates; a pinned Python container does the training via
// KerasNLP.
//
// Dataset schema (JSONL):
//
//	{"prompt":"…", "response":"…"}
//	{"prompt":"…", "response":"…"}
//
// Supported base models: "gemma3:270m", "gemma3:1b", "gemma3:4b",
// "gemma3n:e2b", "gemma3n:e4b". The container resolves the base
// checkpoint at runtime (Hugging Face token expected in env
// HF_TOKEN).
//
// Output: a LoRA weight bundle as a `.keras` archive plus a
// `metrics.json` sidecar. Both are uploaded to the configured
// [storage.Bucket].
package gemma

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

// DefaultImage is the published training image. Override via
// [Options.Image] when developing locally.
const DefaultImage = "ghcr.io/golusoris/tiny-gemma-trainer:v1"

// ArtifactName is the filename the trainer container writes into
// /work/output.
const ArtifactName = "lora.keras"

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
	// (`<prefix>/<name>/v<version>/lora.keras`). Default: "models/gemma".
	KeyPrefix string
	// Image overrides [DefaultImage].
	Image string
	// GPUs passed to the Runner (0 = CPU). Training on CPU is only
	// useful for smoke tests.
	GPUs int
	// Timeout caps a single training run. 0 = no cap beyond ctx.
	Timeout time.Duration
	// ExtraEnv is merged into the Runner env (useful for HF_TOKEN,
	// WANDB_API_KEY, …). Trainer-managed keys (JOB_*, DATASET_*) are
	// not overridable.
	ExtraEnv map[string]string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Trainer is a [tiny.Trainer] that fine-tunes Gemma with LoRA.
type Trainer struct {
	opts Options
}

// NewTrainer validates opts and returns a Trainer.
func NewTrainer(opts Options) (*Trainer, error) {
	if opts.Runner == nil {
		return nil, errors.New("ai/tiny/gemma: Runner required")
	}
	if opts.Bucket == nil {
		return nil, errors.New("ai/tiny/gemma: Bucket required")
	}
	if opts.Image == "" {
		opts.Image = DefaultImage
	}
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "models/gemma"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Trainer{opts: opts}, nil
}

// Name reports "gemma".
func (*Trainer) Name() string { return "gemma" }

// Train stages the dataset + hyperparams, invokes the Runner, and
// uploads the resulting LoRA bundle. The returned [tiny.Model] has a
// zero Version — the Registry assigns it on SaveModel.
func (t *Trainer) Train(ctx context.Context, job tiny.Job) (tiny.Model, error) {
	if err := tiny.ValidateJob(job); err != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/gemma: validate: %w", err)
	}
	if job.Dataset.Modality != tiny.ModalityText || job.Dataset.TaskKind != tiny.TaskGenerate {
		return tiny.Model{}, fmt.Errorf("ai/tiny/gemma: need Modality=text + TaskKind=generate, got %s/%s",
			job.Dataset.Modality, job.Dataset.TaskKind)
	}
	if !strings.HasPrefix(job.BaseModel, "gemma3:") && !strings.HasPrefix(job.BaseModel, "gemma3n:") {
		return tiny.Model{}, fmt.Errorf("ai/tiny/gemma: BaseModel %q: expected gemma3:* or gemma3n:*", job.BaseModel)
	}

	workDir, inputDir, outputDir, err := stageWorkDir(job)
	if err != nil {
		return tiny.Model{}, err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	env := map[string]string{
		"TINY_JOB_NAME":   job.Name,
		"TINY_BASE_MODEL": job.BaseModel,
	}
	for k, v := range t.opts.ExtraEnv {
		if _, reserved := env[k]; !reserved {
			env[k] = v
		}
	}

	t.opts.Logger.InfoContext(ctx, "ai/tiny/gemma: training start",
		slog.String("job", job.Name),
		slog.String("base", job.BaseModel),
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
	// Drain trainer stdout/stderr into structured logs whether or not Run
	// errored — failure output is often more useful than success output.
	sc := bufio.NewScanner(&logBuf)
	for sc.Scan() {
		t.opts.Logger.InfoContext(ctx, "ai/tiny/gemma: trainer", slog.String("line", sc.Text()))
	}
	if runErr != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/gemma: runner: %w", runErr)
	}

	artifactPath := filepath.Join(outputDir, ArtifactName)
	artifactBytes, readErr := os.ReadFile(artifactPath)
	if readErr != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/gemma: read artifact: %w", readErr)
	}
	metrics := map[string]float64{}
	if metricsBytes, mErr := os.ReadFile(filepath.Join(outputDir, MetricsName)); mErr == nil {
		if jerr := json.Unmarshal(metricsBytes, &metrics); jerr != nil {
			t.opts.Logger.WarnContext(ctx, "ai/tiny/gemma: parse metrics", slog.String("error", jerr.Error()))
		}
	}

	// Upload. Registry assigns Version, so the URI embeds the job ID
	// until the caller re-keys after SaveModel — good enough for a
	// content-addressed upload right now.
	key := filepath.ToSlash(filepath.Join(t.opts.KeyPrefix, job.Name, job.ID, ArtifactName))
	obj, err := t.opts.Bucket.Put(ctx, key, strings.NewReader(string(artifactBytes)), storage.PutOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return tiny.Model{}, fmt.Errorf("ai/tiny/gemma: upload: %w", err)
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
		Metadata:  copyStringMap(job.Tags),
	}, nil
}

// stageWorkDir creates a temp work directory with input/ + output/
// subdirs and writes config.json into input/ for the container to read.
func stageWorkDir(job tiny.Job) (workDir, inputDir, outputDir string, err error) {
	workDir, err = os.MkdirTemp("", "tiny-gemma-*")
	if err != nil {
		return "", "", "", fmt.Errorf("ai/tiny/gemma: mktemp: %w", err)
	}
	inputDir = filepath.Join(workDir, "input")
	outputDir = filepath.Join(workDir, "output")
	if mkErr := os.MkdirAll(inputDir, 0o750); mkErr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/gemma: mkdir input: %w", mkErr)
	}
	if mkErr := os.MkdirAll(outputDir, 0o750); mkErr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/gemma: mkdir output: %w", mkErr)
	}
	cfg := map[string]any{
		"job_id":      job.ID,
		"job_name":    job.Name,
		"tenant_id":   job.TenantID,
		"base_model":  job.BaseModel,
		"dataset_uri": job.Dataset.URI,
		"dataset_fmt": job.Dataset.Format,
		"hyperparams": job.Hyperparams,
		"tags":        job.Tags,
	}
	cfgBytes, jerr := json.Marshal(cfg)
	if jerr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/gemma: marshal config: %w", jerr)
	}
	if writeErr := os.WriteFile(filepath.Join(inputDir, "config.json"), cfgBytes, 0o600); writeErr != nil {
		return "", "", "", fmt.Errorf("ai/tiny/gemma: write config: %w", writeErr)
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
