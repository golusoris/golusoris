package tiny_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
)

func TestStubRunner_invokesFn(t *testing.T) {
	t.Parallel()
	called := false
	r := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		called = true
		require.Equal(t, "ghcr.io/test:1", spec.Image)
		return nil
	}}
	err := r.Run(t.Context(), tiny.RunSpec{Image: "ghcr.io/test:1", InputDir: "/in", OutputDir: "/out"})
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "stub", r.Name())
}

func TestStubRunner_nilFn(t *testing.T) {
	t.Parallel()
	r := &tiny.StubRunner{}
	require.NoError(t, r.Run(t.Context(), tiny.RunSpec{Image: "x", InputDir: "i", OutputDir: "o"}))
}

func TestDockerRunner_rejectsMissingImage(t *testing.T) {
	t.Parallel()
	r := &tiny.DockerRunner{}
	err := r.Run(t.Context(), tiny.RunSpec{InputDir: "/a", OutputDir: "/b"})
	require.Error(t, err)
}

func TestDockerRunner_rejectsMissingDirs(t *testing.T) {
	t.Parallel()
	r := &tiny.DockerRunner{}
	err := r.Run(t.Context(), tiny.RunSpec{Image: "x"})
	require.Error(t, err)
}

func TestDockerRunner_nameIsDocker(t *testing.T) {
	t.Parallel()
	require.Equal(t, "docker", (&tiny.DockerRunner{}).Name())
}

// TestStubRunner_writesOutput validates the expected stub pattern:
// the Fn materializes an artifact in spec.OutputDir, which higher-level
// Trainer packages then upload to object storage.
func TestStubRunner_writesOutput(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	r := &tiny.StubRunner{Fn: func(_ context.Context, spec tiny.RunSpec) error {
		return os.WriteFile(filepath.Join(spec.OutputDir, "model.tflite"), []byte{0, 1, 2, 3}, 0o600)
	}}
	require.NoError(t, r.Run(t.Context(), tiny.RunSpec{Image: "x", InputDir: t.TempDir(), OutputDir: out}))
	data, err := os.ReadFile(filepath.Join(out, "model.tflite"))
	require.NoError(t, err)
	require.Len(t, data, 4)
}
