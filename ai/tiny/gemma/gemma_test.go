// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gemma_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/gemma"
	"github.com/golusoris/golusoris/ai/tiny/internal/trainerio"
	"github.com/golusoris/golusoris/internal/fileuri"
	"github.com/golusoris/golusoris/storage"
)

const testPinnedImage = "example.invalid/tiny-gemma:v1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestNewTrainer_requiresRunnerAndBucket(t *testing.T) {
	t.Parallel()
	_, err := gemma.NewTrainer(gemma.Options{})
	require.Error(t, err)

	bucket, bErr := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, bErr)
	_, err = gemma.NewTrainer(gemma.Options{Bucket: bucket})
	require.Error(t, err)

	_, err = gemma.NewTrainer(gemma.Options{Runner: &tiny.StubRunner{}})
	require.Error(t, err)
}

func TestTrainer_Name(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, err := gemma.NewTrainer(gemma.Options{
		Runner:      &tiny.StubRunner{},
		Bucket:      bucket,
		DatasetRoot: canonicalTempDir(t),
		Image:       testPinnedImage,
	})
	require.NoError(t, err)
	require.Equal(t, "gemma", tr.Name())
}

// happyPathRunner simulates the container: reads the job config, checks
// what the trainer staged, and writes a fake LoRA archive + metrics.json.
func happyPathRunner(t *testing.T, wantNetworkDisabled bool) *tiny.StubRunner {
	t.Helper()
	return &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		cfgBytes, readErr := os.ReadFile(filepath.Join(spec.InputDir, "config.json"))
		if readErr != nil {
			return readErr
		}
		var cfg map[string]any
		if jerr := json.Unmarshal(cfgBytes, &cfg); jerr != nil {
			return jerr
		}
		if cfg["job_name"] != "intent-finetune" {
			t.Errorf("expected job_name intent-finetune, got %v", cfg["job_name"])
		}
		if cfg["base_model"] != "gemma3:270m" {
			t.Errorf("expected base_model gemma3:270m, got %v", cfg["base_model"])
		}
		if spec.Env["TINY_BASE_MODEL"] != "gemma3:270m" {
			t.Errorf("expected TINY_BASE_MODEL=gemma3:270m, got %q", spec.Env["TINY_BASE_MODEL"])
		}
		if spec.Env["HF_TOKEN"] != "hf_fake" {
			t.Errorf("expected HF_TOKEN=hf_fake, got %q", spec.Env["HF_TOKEN"])
		}
		if spec.NetworkDisabled != wantNetworkDisabled {
			t.Errorf("NetworkDisabled = %v; want %v", spec.NetworkDisabled, wantNetworkDisabled)
		}
		if werr := os.WriteFile(filepath.Join(spec.OutputDir, gemma.ArtifactName), []byte("fake-lora-bytes"), 0o600); werr != nil {
			return werr
		}
		b, err := json.Marshal(map[string]float64{"loss": 0.42, "epochs": 3})
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(spec.OutputDir, gemma.MetricsName), b, 0o600)
	}}
}

func happyPathJob(t *testing.T) (tiny.Job, string) {
	t.Helper()
	datasetRoot := canonicalTempDir(t)
	tenantRoot := filepath.Join(datasetRoot, "tenant-a")
	require.NoError(t, os.Mkdir(tenantRoot, 0o700))
	dataset := filepath.Join(tenantRoot, "data.jsonl")
	require.NoError(t, os.WriteFile(dataset, []byte("{\"prompt\":\"p\",\"response\":\"r\"}\n"), 0o600))
	datasetURI, err := fileuri.FromPath(dataset)
	require.NoError(t, err)
	return tiny.Job{
		ID:        "job-1",
		Name:      "intent-finetune",
		TenantID:  "tenant-a",
		BaseModel: "gemma3:270m",
		Dataset: tiny.Dataset{
			URI:      datasetURI,
			Format:   "jsonl",
			Modality: tiny.ModalityText,
			TaskKind: tiny.TaskGenerate,
		},
		Hyperparams: map[string]any{"learning_rate": 0.0002, "epochs": 3, "lora_rank": 8},
		Tags:        map[string]string{"env": "dev"},
	}, datasetRoot
}

func TestTrainer_Train_happyPath(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := happyPathJob(t)

	tr, err := gemma.NewTrainer(gemma.Options{
		Runner:      happyPathRunner(t, true),
		Bucket:      bucket,
		DatasetRoot: datasetRoot,
		Image:       testPinnedImage,
		KeyPrefix:   "models/gemma",
		ExtraEnv:    map[string]string{"HF_TOKEN": "hf_fake"},
	})
	require.NoError(t, err)

	got, err := tr.Train(t.Context(), job)
	require.NoError(t, err)

	require.Equal(t, "intent-finetune", got.Name)
	require.Equal(t, tiny.FormatKerasLoRA, got.Format)
	require.Equal(t, tiny.TaskGenerate, got.TaskKind)
	require.Equal(t, "gemma3:270m", got.BaseModel)
	require.Equal(t, "tenant-a", got.TenantID)
	require.InDelta(t, 0.42, got.Metrics["loss"], 1e-9)
	require.Equal(t, "dev", got.Metadata["env"])
	require.Contains(t, got.URI, "intent-finetune")
	require.Contains(t, got.URI, "/tenants/tenant-a/")

	// Verify the artifact actually landed in the bucket.
	rc, _, err := bucket.Get(t.Context(), got.URI)
	require.NoError(t, err)
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, "fake-lora-bytes", string(body))
}

func TestTrainer_Train_allowsExplicitPresetNetwork(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := happyPathJob(t)
	trainer, err := gemma.NewTrainer(gemma.Options{
		Runner: happyPathRunner(t, false), Bucket: bucket, DatasetRoot: datasetRoot,
		Image: testPinnedImage, AllowNetwork: true, ExtraEnv: map[string]string{"HF_TOKEN": "hf_fake"},
	})
	require.NoError(t, err)
	_, err = trainer.Train(t.Context(), job)
	require.NoError(t, err)
}

func TestTrainer_Train_rejectsWrongModality(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, _ := gemma.NewTrainer(gemma.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t), Image: testPinnedImage,
	})

	_, err = tr.Train(t.Context(), tiny.Job{
		ID:        "job-wrong-modality",
		Name:      "x",
		BaseModel: "gemma3:270m",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityImage, TaskKind: tiny.TaskGenerate},
	})
	require.ErrorContains(t, err, "Modality=text")
}

func TestTrainer_Train_rejectsNonGemmaBase(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, _ := gemma.NewTrainer(gemma.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t), Image: testPinnedImage,
	})

	_, err = tr.Train(t.Context(), tiny.Job{
		ID:        "job-wrong-base",
		Name:      "x",
		BaseModel: "llama3:8b",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskGenerate},
	})
	require.ErrorContains(t, err, "not supported")
}

func TestTrainer_Train_missingArtifactIsError(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	// Runner that writes nothing.
	runner := &tiny.StubRunner{Fn: func(_ context.Context, _ tiny.RunSpec) error { return nil }}
	job, datasetRoot := happyPathJob(t)
	job.ID = "job-missing-artifact"
	job.Name = "x"
	tr, _ := gemma.NewTrainer(gemma.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
	})

	_, err = tr.Train(t.Context(), job)
	require.Error(t, err)
	require.ErrorContains(t, err, "artifact")
}

func TestNewTrainer_requiresPinnedImageOrExplicitTrust(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	base := gemma.Options{Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t)}
	_, err = gemma.NewTrainer(base)
	require.ErrorContains(t, err, "Image required")
	base.Image = "example.invalid/tiny-gemma:v1"
	_, err = gemma.NewTrainer(base)
	require.ErrorContains(t, err, "sha256")
	base.AllowUnpinnedImage = true
	_, err = gemma.NewTrainer(base)
	require.NoError(t, err)
	base.Image = testPinnedImage
	base.AllowUnpinnedImage = false
	base.Timeout = -time.Second
	_, err = gemma.NewTrainer(base)
	require.ErrorContains(t, err, "must not be negative")
	base.Timeout = 0
	base.GPUs = -1
	_, err = gemma.NewTrainer(base)
	require.ErrorContains(t, err, "GPUs must not be negative")
}

func TestNewTrainer_requiresCanonicalDatasetRoot(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	base := gemma.Options{Runner: &tiny.StubRunner{}, Bucket: bucket, Image: testPinnedImage}
	_, err = gemma.NewTrainer(base)
	require.ErrorContains(t, err, "DatasetRoot required")
	base.DatasetRoot = "relative"
	_, err = gemma.NewTrainer(base)
	require.ErrorContains(t, err, "absolute canonical path")
}

func TestTrainer_Train_rejectsRemoteDatasetBeforeRunner(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	called := false
	runner := &tiny.StubRunner{Fn: func(_ context.Context, _ tiny.RunSpec) error {
		called = true
		return nil
	}}
	job, datasetRoot := happyPathJob(t)
	trainer, err := gemma.NewTrainer(gemma.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
	})
	require.NoError(t, err)
	for _, datasetURI := range []string{"https://example.invalid/data.jsonl", "s3://bucket/data.jsonl"} {
		job.Dataset.URI = datasetURI
		_, trainErr := trainer.Train(t.Context(), job)
		require.ErrorContains(t, trainErr, "not supported")
	}
	require.False(t, called)
}

func TestTrainer_TrainBoundsRunnerContext(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := happyPathJob(t)
	var gotDeadline time.Time
	runner := &tiny.StubRunner{Fn: func(ctx context.Context, spec tiny.RunSpec) error {
		var ok bool
		gotDeadline, ok = ctx.Deadline()
		if !ok {
			return errors.New("runner context has no deadline")
		}
		return os.WriteFile(filepath.Join(spec.OutputDir, gemma.ArtifactName), []byte("ok"), 0o600)
	}}
	trainer, err := gemma.NewTrainer(gemma.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
	})
	require.NoError(t, err)
	_, err = trainer.Train(t.Context(), job)
	require.NoError(t, err)
	remaining := time.Until(gotDeadline)
	require.Positive(t, remaining)
	require.LessOrEqual(t, remaining, tiny.DefaultRunTimeout)

	parent, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	parentDeadline, _ := parent.Deadline()
	_, err = trainer.Train(parent, job)
	require.NoError(t, err)
	require.Equal(t, parentDeadline, gotDeadline)
}

func TestTrainer_Train_rejectsOversizedOutputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		artifact    string
		metrics     string
		artifactCap int64
		metricsCap  int64
	}{
		{name: "artifact", artifact: "12345", artifactCap: 4, metricsCap: 64},
		{name: "metrics", artifact: "ok", metrics: "12345", artifactCap: 64, metricsCap: 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bucket, err := storage.NewLocalBucket(t.TempDir())
			require.NoError(t, err)
			job, datasetRoot := happyPathJob(t)
			runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
				if writeErr := os.WriteFile(filepath.Join(spec.OutputDir, gemma.ArtifactName), []byte(tc.artifact), 0o600); writeErr != nil {
					return writeErr
				}
				if tc.metrics == "" {
					return nil
				}
				return os.WriteFile(filepath.Join(spec.OutputDir, gemma.MetricsName), []byte(tc.metrics), 0o600)
			}}
			trainer, newErr := gemma.NewTrainer(gemma.Options{
				Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
				MaxArtifactBytes: tc.artifactCap, MaxMetricsBytes: tc.metricsCap,
			})
			require.NoError(t, newErr)
			_, trainErr := trainer.Train(t.Context(), job)
			require.ErrorIs(t, trainErr, trainerio.ErrOutputTooLarge)
		})
	}
}

func TestTrainer_Train_boundsContainerLogs(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := happyPathJob(t)
	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		_, writeErr := spec.Logger.Write([]byte("0123456789-secret-tail"))
		if writeErr != nil {
			return writeErr
		}
		return os.WriteFile(filepath.Join(spec.OutputDir, gemma.ArtifactName), []byte("ok"), 0o600)
	}}
	var logs bytes.Buffer
	trainer, err := gemma.NewTrainer(gemma.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot,
		Image: testPinnedImage, MaxLogBytes: 8,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	require.NoError(t, err)
	_, err = trainer.Train(t.Context(), job)
	require.NoError(t, err)
	require.Contains(t, logs.String(), "truncated")
	require.NotContains(t, logs.String(), "secret-tail")
}

func TestTrainer_Train_cleansFailedStage(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := happyPathJob(t)
	trainer, err := gemma.NewTrainer(gemma.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
	})
	require.NoError(t, err)
	job.Hyperparams = map[string]any{"invalid": func() {}}
	_, err = trainer.Train(t.Context(), job)
	require.Error(t, err)
	var unsupported *json.UnsupportedTypeError
	require.True(t, errors.As(err, &unsupported))
	entries, err := os.ReadDir(tempRoot)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return root
}
