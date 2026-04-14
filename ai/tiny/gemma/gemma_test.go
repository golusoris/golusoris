package gemma_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/gemma"
	"github.com/golusoris/golusoris/storage"
)

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
		Runner: &tiny.StubRunner{},
		Bucket: bucket,
	})
	require.NoError(t, err)
	require.Equal(t, "gemma", tr.Name())
}

func TestTrainer_Train_happyPath(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)

	// StubRunner simulates the container: reads the job config,
	// writes a fake LoRA archive + metrics.json.
	runner := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		// Validate config.json is present in InputDir.
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
		// Env is expected to carry TINY_BASE_MODEL.
		if spec.Env["TINY_BASE_MODEL"] != "gemma3:270m" {
			t.Errorf("expected TINY_BASE_MODEL=gemma3:270m, got %q", spec.Env["TINY_BASE_MODEL"])
		}
		if spec.Env["HF_TOKEN"] != "hf_fake" {
			t.Errorf("expected HF_TOKEN=hf_fake, got %q", spec.Env["HF_TOKEN"])
		}
		// Write fake artifact + metrics.
		if werr := os.WriteFile(filepath.Join(spec.OutputDir, gemma.ArtifactName), []byte("fake-lora-bytes"), 0o600); werr != nil {
			return werr
		}
		m := map[string]float64{"loss": 0.42, "epochs": 3}
		b, _ := json.Marshal(m)
		return os.WriteFile(filepath.Join(spec.OutputDir, gemma.MetricsName), b, 0o600)
	}}

	tr, err := gemma.NewTrainer(gemma.Options{
		Runner:    runner,
		Bucket:    bucket,
		KeyPrefix: "models/gemma",
		ExtraEnv:  map[string]string{"HF_TOKEN": "hf_fake"},
	})
	require.NoError(t, err)

	job := tiny.Job{
		ID:        "job-1",
		Name:      "intent-finetune",
		TenantID:  "tenant-a",
		BaseModel: "gemma3:270m",
		Dataset: tiny.Dataset{
			URI:      "file:///tmp/data.jsonl",
			Format:   "jsonl",
			Modality: tiny.ModalityText,
			TaskKind: tiny.TaskGenerate,
		},
		Hyperparams: map[string]any{"lr": 0.0002, "epochs": 3, "lora_rank": 8},
		Tags:        map[string]string{"env": "dev"},
	}
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

	// Verify the artifact actually landed in the bucket.
	rc, _, err := bucket.Get(t.Context(), got.URI)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "fake-lora-bytes", string(body))
}

func TestTrainer_Train_rejectsWrongModality(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	tr, _ := gemma.NewTrainer(gemma.Options{Runner: &tiny.StubRunner{}, Bucket: bucket})

	_, err = tr.Train(t.Context(), tiny.Job{
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
	tr, _ := gemma.NewTrainer(gemma.Options{Runner: &tiny.StubRunner{}, Bucket: bucket})

	_, err = tr.Train(t.Context(), tiny.Job{
		Name:      "x",
		BaseModel: "llama3:8b",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskGenerate},
	})
	require.ErrorContains(t, err, "expected gemma3")
}

func TestTrainer_Train_missingArtifactIsError(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	require.NoError(t, err)
	// Runner that writes nothing.
	runner := &tiny.StubRunner{Fn: func(_ context.Context, _ tiny.RunSpec) error { return nil }}
	tr, _ := gemma.NewTrainer(gemma.Options{Runner: runner, Bucket: bucket})

	_, err = tr.Train(t.Context(), tiny.Job{
		Name:      "x",
		BaseModel: "gemma3:270m",
		Dataset:   tiny.Dataset{URI: "u", Modality: tiny.ModalityText, TaskKind: tiny.TaskGenerate},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "read artifact")
}
