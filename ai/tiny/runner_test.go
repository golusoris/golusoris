package tiny_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestDockerRunner_buildsArgs exercises the arg-composition path by
// pointing DockerPath at /bin/true (accepts any args, exits 0). This
// covers the full Run path including Pull/Network/UserNSRemap/GPUs/Env
// branches without requiring Docker.
func TestDockerRunner_buildsArgs(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/bin/true"); err != nil {
		t.Skip("/bin/true unavailable")
	}
	r := &tiny.DockerRunner{
		DockerPath:  "/bin/true",
		Pull:        "missing",
		Network:     "host",
		UserNSRemap: true,
	}
	err := r.Run(t.Context(), tiny.RunSpec{
		Image:     "ghcr.io/test:1",
		Env:       map[string]string{"K": "V"},
		InputDir:  t.TempDir(),
		OutputDir: t.TempDir(),
		GPUs:      1,
	})
	require.NoError(t, err)
}

// TestDockerRunner_execFailure surfaces non-zero exits as wrapped errors.
func TestDockerRunner_execFailure(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/bin/false"); err != nil {
		t.Skip("/bin/false unavailable")
	}
	r := &tiny.DockerRunner{DockerPath: "/bin/false"}
	err := r.Run(t.Context(), tiny.RunSpec{
		Image:     "x",
		InputDir:  t.TempDir(),
		OutputDir: t.TempDir(),
	})
	require.ErrorContains(t, err, "docker run")
}

// TestDockerRunner_timeoutWrapsCtx ensures the spec Timeout derives a
// sub-context (covers the `if spec.Timeout > 0` branch).
func TestDockerRunner_timeoutWrapsCtx(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("/bin/true"); err != nil {
		t.Skip("/bin/true unavailable")
	}
	r := &tiny.DockerRunner{DockerPath: "/bin/true"}
	err := r.Run(t.Context(), tiny.RunSpec{
		Image:     "x",
		InputDir:  t.TempDir(),
		OutputDir: t.TempDir(),
		Timeout:   5 * time.Second,
	})
	require.NoError(t, err)
}
