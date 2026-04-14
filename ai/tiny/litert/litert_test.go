package litert_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/litert"
	"github.com/golusoris/golusoris/storage"
)

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
	tr, err := litert.NewTrainer(litert.Options{Runner: &tiny.StubRunner{}, Bucket: bucket})
	require.NoError(t, err)
	require.Equal(t, "litert", tr.Name())
}

func TestTrainer_Train_happyPath(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)

	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		cfgBytes, readErr := os.ReadFile(filepath.Join(spec.InputDir, "config.json"))
		if readErr != nil {
			return readErr
		}
		var cfg map[string]any
		if jerr := json.Unmarshal(cfgBytes, &cfg); jerr != nil {
			return jerr
		}
		if cfg["modality"] != "text" {
			t.Errorf("expected modality text, got %v", cfg["modality"])
		}
		if cfg["task_kind"] != "classify" {
			t.Errorf("expected task_kind classify, got %v", cfg["task_kind"])
		}
		if spec.Env["TINY_MODALITY"] != "text" {
			t.Errorf("expected TINY_MODALITY=text, got %q", spec.Env["TINY_MODALITY"])
		}
		if werr := os.WriteFile(filepath.Join(spec.OutputDir, litert.ArtifactName), []byte("fake-tflite"), 0o600); werr != nil {
			return werr
		}
		sidecar := struct {
			Metrics map[string]float64 `json:"metrics"`
			Labels  []string           `json:"labels"`
		}{
			Metrics: map[string]float64{"accuracy": 0.93, "loss": 0.18},
			Labels:  []string{"spam", "ham"},
		}
		b, _ := json.Marshal(sidecar)
		return os.WriteFile(filepath.Join(spec.OutputDir, litert.MetricsName), b, 0o600)
	}}

	tr, err := litert.NewTrainer(litert.Options{Runner: runner, Bucket: bucket})
	require.NoError(t, err)

	job := tiny.Job{
		ID:        "job-lt-1",
		Name:      "spam-classifier",
		TenantID:  "tenant-a",
		BaseModel: "mediapipe:text_classifier",
		Dataset: tiny.Dataset{
			URI:      "file:///tmp/corpus.tgz",
			Format:   "tar.gz",
			Modality: tiny.ModalityText,
			TaskKind: tiny.TaskClassify,
		},
		Hyperparams: map[string]any{"epochs": 3, "batch_size": 16},
		Tags:        map[string]string{"env": "dev"},
	}
	got, err := tr.Train(t.Context(), job)
	require.NoError(t, err)
	require.Equal(t, tiny.FormatTFLite, got.Format)
	require.Equal(t, tiny.ModalityText, got.Modality)
	require.Equal(t, tiny.TaskClassify, got.TaskKind)
	require.Equal(t, []string{"spam", "ham"}, got.Labels)
	require.InDelta(t, 0.93, got.Metrics["accuracy"], 1e-9)
	require.Equal(t, "dev", got.Metadata["env"])
	require.Contains(t, got.URI, "spam-classifier")

	rc, _, err := bucket.Get(t.Context(), got.URI)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "fake-tflite", string(body))
}

func TestTrainer_Train_rejectsNonClassify(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, _ := litert.NewTrainer(litert.Options{Runner: &tiny.StubRunner{}, Bucket: bucket})

	_, err = tr.Train(t.Context(), tiny.Job{
		Name:      "x",
		BaseModel: "mediapipe:text_classifier",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskGenerate},
	})
	require.ErrorContains(t, err, "TaskKind=classify")
}

func TestTrainer_Train_rejectsUnsupportedModality(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, _ := litert.NewTrainer(litert.Options{Runner: &tiny.StubRunner{}, Bucket: bucket})

	_, err = tr.Train(t.Context(), tiny.Job{
		Name:      "x",
		BaseModel: "mediapipe:text_classifier",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.Modality("video"), TaskKind: tiny.TaskClassify},
	})
	require.ErrorContains(t, err, "not supported")
}

func TestTrainer_Train_missingArtifactIsError(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	runner := &tiny.StubRunner{Fn: func(_ context.Context, _ tiny.RunSpec) error { return nil }}
	tr, _ := litert.NewTrainer(litert.Options{Runner: runner, Bucket: bucket})

	_, err = tr.Train(t.Context(), tiny.Job{
		Name:      "x",
		BaseModel: "mediapipe:text_classifier",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskClassify},
	})
	require.ErrorContains(t, err, "read artifact")
}
