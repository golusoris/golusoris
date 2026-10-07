// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package litert_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/internal/trainerio"
	"github.com/golusoris/golusoris/ai/tiny/litert"
	"github.com/golusoris/golusoris/storage"
)

const testPinnedImage = "example.invalid/tiny-litert:v1@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestNewTrainer_requiresRunnerAndBucket(t *testing.T) {
	t.Parallel()
	_, err := litert.NewTrainer(litert.Options{})
	require.Error(t, err)

	bucket, bErr := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, bErr)
	_, err = litert.NewTrainer(litert.Options{Bucket: bucket})
	require.Error(t, err)

	_, err = litert.NewTrainer(litert.Options{Runner: &tiny.StubRunner{}})
	require.Error(t, err)
}

func TestTrainer_Name(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, err := litert.NewTrainer(litert.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t), Image: testPinnedImage,
	})
	require.NoError(t, err)
	require.Equal(t, "litert", tr.Name())
}

func TestTrainer_Train_happyPath(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := validJob(t)

	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		cfgBytes, readErr := os.ReadFile(filepath.Join(spec.InputDir, "config.json"))
		if readErr != nil {
			return readErr
		}
		var cfg map[string]any
		if jerr := json.Unmarshal(cfgBytes, &cfg); jerr != nil {
			return jerr
		}
		if cfg["modality"] != "image" {
			t.Errorf("expected modality image, got %v", cfg["modality"])
		}
		if cfg["task_kind"] != "classify" {
			t.Errorf("expected task_kind classify, got %v", cfg["task_kind"])
		}
		if spec.Env["TINY_MODALITY"] != "image" {
			t.Errorf("expected TINY_MODALITY=image, got %q", spec.Env["TINY_MODALITY"])
		}
		if !spec.NetworkDisabled {
			t.Error("LiteRT runner did not disable network access")
		}
		if spec.MaxOutputFileBytes != litert.DefaultMaxArtifactBytes {
			t.Errorf("output file cap = %d; want %d", spec.MaxOutputFileBytes, litert.DefaultMaxArtifactBytes)
		}
		const wantTmpfsBytes = int64(11 << 30)
		if spec.TmpfsBytes != wantTmpfsBytes {
			t.Errorf("tmpfs budget = %d; want %d", spec.TmpfsBytes, wantTmpfsBytes)
		}
		if werr := os.WriteFile(filepath.Join(spec.OutputDir, litert.ArtifactName), []byte("fake-tflite"), 0o600); werr != nil {
			return werr
		}
		sidecar := struct {
			Metrics map[string]float64 `json:"metrics"`
			Labels  []string           `json:"labels"`
		}{
			Metrics: map[string]float64{"accuracy": 0.93, "loss": 0.18},
			Labels:  []string{"cats", "dogs"},
		}
		b, _ := json.Marshal(sidecar)
		return os.WriteFile(filepath.Join(spec.OutputDir, litert.MetricsName), b, 0o600)
	}}

	tr, err := litert.NewTrainer(litert.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
	})
	require.NoError(t, err)
	job.Hyperparams = map[string]any{"epochs": 3, "batch_size": 16}
	job.Tags = map[string]string{"env": "dev"}
	got, err := tr.Train(t.Context(), job)
	require.NoError(t, err)
	require.Equal(t, tiny.FormatTFLite, got.Format)
	require.Equal(t, tiny.ModalityImage, got.Modality)
	require.Equal(t, tiny.TaskClassify, got.TaskKind)
	require.Equal(t, []string{"cats", "dogs"}, got.Labels)
	require.InDelta(t, 0.93, got.Metrics["accuracy"], 1e-9)
	require.Equal(t, "dev", got.Metadata["env"])
	require.Contains(t, got.URI, "pet-classifier")
	require.Contains(t, got.URI, "/tenants/tenant-a/")

	rc, _, err := bucket.Get(t.Context(), got.URI)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "fake-tflite", string(body))
}

func TestNewTrainerBoundsLiteRTDatasetForWorkspace(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	base := litert.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t),
		Image: testPinnedImage,
	}
	base.MaxDatasetBytes = litert.DefaultMaxDatasetBytes
	_, err = litert.NewTrainer(base)
	require.NoError(t, err)
	base.MaxDatasetBytes++
	_, err = litert.NewTrainer(base)
	require.ErrorContains(t, err, "MaxDatasetBytes")
}

func TestTrainerDerivesTmpfsBudgetFromDatasetCap(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := validJob(t)
	const datasetCap = int64(4096)
	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		require.Equal(t, datasetCap+(1<<30), spec.TmpfsBytes)
		if writeErr := os.WriteFile(
			filepath.Join(spec.OutputDir, litert.ArtifactName), []byte("ok"), 0o600,
		); writeErr != nil {
			return writeErr
		}
		return writeValidSidecar(spec.OutputDir)
	}}
	trainer, err := litert.NewTrainer(litert.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
		MaxDatasetBytes: datasetCap,
	})
	require.NoError(t, err)
	_, err = trainer.Train(t.Context(), job)
	require.NoError(t, err)
}

func TestTrainer_Train_rejectsNonClassify(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, _ := litert.NewTrainer(litert.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t), Image: testPinnedImage,
	})

	_, err = tr.Train(t.Context(), tiny.Job{
		ID:        "job-wrong-task",
		Name:      "x",
		BaseModel: litert.BaseImageMobileNetV2,
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityImage, TaskKind: tiny.TaskGenerate},
	})
	require.ErrorContains(t, err, "TaskKind=classify")
}

func TestTrainer_Train_rejectsUnsupportedModality(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, _ := litert.NewTrainer(litert.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t), Image: testPinnedImage,
	})

	_, err = tr.Train(t.Context(), tiny.Job{
		ID:        "job-wrong-modality",
		Name:      "x",
		BaseModel: litert.BaseImageMobileNetV2,
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.Modality("video"), TaskKind: tiny.TaskClassify},
	})
	require.ErrorContains(t, err, "requires Modality=image")

	_, err = tr.Train(t.Context(), tiny.Job{
		ID:        "job-audio",
		Name:      "audio",
		BaseModel: "mediapipe:audio/yamnet",
		Dataset: tiny.Dataset{
			URI: "https://example.invalid/audio.tar.gz", Format: "tar.gz",
			Modality: tiny.ModalityAudio, TaskKind: tiny.TaskClassify,
		},
	})
	require.ErrorContains(t, err, "BaseModel")
	require.ErrorContains(t, err, "not supported")
}

func TestTrainer_Train_rejectsRemovedModelIdentifiers(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, err := litert.NewTrainer(litert.Options{
		Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t), Image: testPinnedImage,
	})
	require.NoError(t, err)
	removed := []string{
		litert.BaseTextAverageWordEmbedding,
		litert.LegacyBaseTextAverageWordEmbedding,
		litert.BaseTextMobileBERT,
		litert.LegacyBaseImageMobileNetV2,
		litert.BaseImageMobileNetV2Keras,
		litert.BaseImageEfficientNetLite0,
		litert.BaseImageEfficientNetLite2,
		litert.BaseImageEfficientNetLite4,
	}
	for _, baseModel := range removed {
		job, _ := validJob(t)
		job.BaseModel = baseModel
		_, trainErr := tr.Train(t.Context(), job)
		require.ErrorContains(t, trainErr, "not supported", baseModel)
	}
}

func TestTrainer_Train_missingArtifactIsError(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		return writeValidSidecar(spec.OutputDir)
	}}
	job, datasetRoot := validJob(t)
	job.ID = "job-missing-artifact"
	job.Name = "x"
	tr, _ := litert.NewTrainer(litert.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
	})

	_, err = tr.Train(t.Context(), job)
	require.ErrorContains(t, err, "artifact")
}

func TestTrainer_Train_rejectsInvalidLabelSidecar(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sidecar string
		want    string
	}{
		{name: "missing", want: "metrics.json"},
		{name: "malformed", sidecar: `{`, want: "parse metrics"},
		{name: "one label", sidecar: `{"labels":["cat"]}`, want: "at least two labels"},
		{name: "blank label", sidecar: `{"labels":["cat"," "]}`, want: "non-empty"},
		{name: "duplicate label", sidecar: `{"labels":["cat","cat"]}`, want: "unique"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bucket, err := storage.NewLocalBucket(t.TempDir())
			require.NoError(t, err)
			job, datasetRoot := validJob(t)
			runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
				if writeErr := os.WriteFile(filepath.Join(spec.OutputDir, litert.ArtifactName), []byte("ok"), 0o600); writeErr != nil {
					return writeErr
				}
				if tc.sidecar == "" {
					return nil
				}
				return os.WriteFile(filepath.Join(spec.OutputDir, litert.MetricsName), []byte(tc.sidecar), 0o600)
			}}
			trainer, newErr := litert.NewTrainer(litert.Options{
				Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
			})
			require.NoError(t, newErr)
			_, trainErr := trainer.Train(t.Context(), job)
			require.ErrorContains(t, trainErr, tc.want)
		})
	}
}

func TestNewTrainer_requiresPinnedImageOrExplicitTrust(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	base := litert.Options{Runner: &tiny.StubRunner{}, Bucket: bucket, DatasetRoot: canonicalTempDir(t)}
	_, err = litert.NewTrainer(base)
	require.ErrorContains(t, err, "Image required")
	base.Image = "example.invalid/tiny-litert:v1"
	_, err = litert.NewTrainer(base)
	require.ErrorContains(t, err, "sha256")
	base.AllowUnpinnedImage = true
	_, err = litert.NewTrainer(base)
	require.NoError(t, err)
	base.Image = testPinnedImage
	base.AllowUnpinnedImage = false
	base.Timeout = -time.Second
	_, err = litert.NewTrainer(base)
	require.ErrorContains(t, err, "must not be negative")
	base.Timeout = 0
	base.GPUs = -1
	_, err = litert.NewTrainer(base)
	require.ErrorContains(t, err, "GPUs must not be negative")
}

func TestNewTrainer_requiresCanonicalDatasetRoot(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	base := litert.Options{Runner: &tiny.StubRunner{}, Bucket: bucket, Image: testPinnedImage}
	_, err = litert.NewTrainer(base)
	require.ErrorContains(t, err, "DatasetRoot required")
	base.DatasetRoot = "relative"
	_, err = litert.NewTrainer(base)
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
	job, datasetRoot := validJob(t)
	trainer, err := litert.NewTrainer(litert.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
	})
	require.NoError(t, err)
	for _, datasetURI := range []string{"https://example.invalid/images.tar", "s3://bucket/images.tar"} {
		job.Dataset.URI = datasetURI
		_, trainErr := trainer.Train(t.Context(), job)
		require.ErrorContains(t, trainErr, "not supported")
	}
	require.False(t, called)
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
			job, datasetRoot := validJob(t)
			runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
				if writeErr := os.WriteFile(filepath.Join(spec.OutputDir, litert.ArtifactName), []byte(tc.artifact), 0o600); writeErr != nil {
					return writeErr
				}
				if tc.metrics == "" {
					return writeValidSidecar(spec.OutputDir)
				}
				return os.WriteFile(filepath.Join(spec.OutputDir, litert.MetricsName), []byte(tc.metrics), 0o600)
			}}
			trainer, newErr := litert.NewTrainer(litert.Options{
				Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
				MaxArtifactBytes: tc.artifactCap, MaxMetricsBytes: tc.metricsCap,
			})
			require.NoError(t, newErr)
			_, trainErr := trainer.Train(t.Context(), job)
			require.ErrorIs(t, trainErr, trainerio.ErrOutputTooLarge)
		})
	}
}

func TestTrainer_Train_acceptsArtifactAtExactCap(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := validJob(t)
	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		if writeErr := os.WriteFile(filepath.Join(spec.OutputDir, litert.ArtifactName), []byte("1234"), 0o600); writeErr != nil {
			return writeErr
		}
		return writeValidSidecar(spec.OutputDir)
	}}
	trainer, err := litert.NewTrainer(litert.Options{
		Runner: runner, Bucket: bucket, DatasetRoot: datasetRoot, Image: testPinnedImage,
		MaxArtifactBytes: 4,
	})
	require.NoError(t, err)
	_, err = trainer.Train(t.Context(), job)
	require.NoError(t, err)
}

func TestTrainer_Train_boundsContainerLogs(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	job, datasetRoot := validJob(t)
	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		_, writeErr := spec.Logger.Write([]byte("0123456789-secret-tail"))
		if writeErr != nil {
			return writeErr
		}
		if writeErr = os.WriteFile(filepath.Join(spec.OutputDir, litert.ArtifactName), []byte("ok"), 0o600); writeErr != nil {
			return writeErr
		}
		return writeValidSidecar(spec.OutputDir)
	}}
	var logs bytes.Buffer
	trainer, err := litert.NewTrainer(litert.Options{
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
	job, datasetRoot := validJob(t)
	trainer, err := litert.NewTrainer(litert.Options{
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

func validJob(t *testing.T) (tiny.Job, string) {
	t.Helper()
	datasetRoot := canonicalTempDir(t)
	tenantRoot := filepath.Join(datasetRoot, "tenant-a")
	require.NoError(t, os.Mkdir(tenantRoot, 0o700))
	dataset := filepath.Join(tenantRoot, "images.tar")
	require.NoError(t, os.WriteFile(dataset, []byte("bounded-image-archive"), 0o600))
	return tiny.Job{
		ID: "job-lt-1", Name: "pet-classifier", TenantID: "tenant-a",
		BaseModel: litert.BaseImageMobileNetV2,
		Dataset: tiny.Dataset{
			URI: (&url.URL{Scheme: "file", Path: dataset}).String(), Format: "tar",
			Modality: tiny.ModalityImage, TaskKind: tiny.TaskClassify,
		},
	}, datasetRoot
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return root
}

func writeValidSidecar(outputDir string) error {
	return os.WriteFile(
		filepath.Join(outputDir, litert.MetricsName),
		[]byte(`{"metrics":{},"labels":["cat","dog"]}`),
		0o600,
	)
}
