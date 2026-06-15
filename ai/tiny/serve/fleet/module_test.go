package fleet

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/jobs"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	require.True(t, o.Enabled)
	require.Equal(t, DefaultQueuePrefix, o.QueuePrefix)
	require.Equal(t, []string{"cpu"}, o.Capabilities)
	require.Equal(t, 4, o.MaxWorkers)
	require.Equal(t, 60*time.Second, o.PredictTimeout)
	require.Equal(t, DefaultMaxInputBytes, o.MaxInputBytes)
}

func TestWithDefaults_fillsZeros(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	require.Equal(t, DefaultQueuePrefix, got.QueuePrefix)
	require.Equal(t, []string{"cpu"}, got.Capabilities)
	require.Equal(t, 4, got.MaxWorkers)
	require.Equal(t, 60*time.Second, got.PredictTimeout)
	require.Equal(t, DefaultMaxInputBytes, got.MaxInputBytes)
}

func TestWithDefaults_keepsExplicit(t *testing.T) {
	t.Parallel()
	in := Options{
		QueuePrefix:    "infer",
		Capabilities:   []string{"gpu"},
		MaxWorkers:     16,
		PredictTimeout: 5 * time.Second,
		MaxInputBytes:  4096,
	}
	got := in.withDefaults()
	require.Equal(t, "infer", got.QueuePrefix)
	require.Equal(t, []string{"gpu"}, got.Capabilities)
	require.Equal(t, 16, got.MaxWorkers)
	require.Equal(t, 5*time.Second, got.PredictTimeout)
	require.Equal(t, 4096, got.MaxInputBytes)
}

func TestWithDefaults_clampsSubOneMaxWorkers(t *testing.T) {
	t.Parallel()
	require.Equal(t, 4, Options{MaxWorkers: -3}.withDefaults().MaxWorkers)
}

func TestLoadOptions_defaultsWhenUnset(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "APP_"})
	require.NoError(t, err)

	got, err := loadOptions(cfg)
	require.NoError(t, err)
	require.True(t, got.Enabled)
	require.Equal(t, "tiny", got.QueuePrefix)
}

func TestLoadOptions_readsFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"tiny:\n  fleet:\n    queue_prefix: infer\n    max_workers: 9\n    capabilities: [gpu, cpu]\n",
	), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)

	got, err := loadOptions(cfg)
	require.NoError(t, err)
	require.Equal(t, "infer", got.QueuePrefix)
	require.Equal(t, 9, got.MaxWorkers)
	require.Equal(t, []string{"gpu", "cpu"}, got.Capabilities)
}

func TestOptionsCapabilities_convertsToTyped(t *testing.T) {
	t.Parallel()
	caps := Options{Capabilities: []string{"cpu", "gpu"}}.capabilities()
	require.Equal(t, []Capability{"cpu", "gpu"}, caps)
}

func TestNewFleet_internal_rejectsNilClient(t *testing.T) {
	t.Parallel()
	reg := tiny.NewMemoryRegistry()
	_, err := newFleet(DefaultOptions(), reg, nil)
	require.ErrorContains(t, err, "nil jobs.Client")
}

func TestRegisterWorker_rejectsMalformedCapability(t *testing.T) {
	t.Parallel()
	reg := tiny.NewMemoryRegistry()
	opts := DefaultOptions()
	opts.Capabilities = []string{"bad/cap"}
	err := registerWorker(opts, reg, SingletonFactory(nopPredictor{}), nopSink{}, jobs.NewWorkers(), slog.New(slog.DiscardHandler))
	require.ErrorContains(t, err, "must be lowercase")
}

func TestRegisterWorker_happyPath(t *testing.T) {
	t.Parallel()
	reg := tiny.NewMemoryRegistry()
	err := registerWorker(DefaultOptions(), reg, SingletonFactory(nopPredictor{}), nopSink{}, jobs.NewWorkers(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
}

// nopPredictor + nopSink + newWorkers keep the internal wiring tests free
// of the river harness — they only exercise option/constructor paths.
type nopPredictor struct{}

func (nopPredictor) Load(context.Context, tiny.Model) error { return nil }
func (nopPredictor) Predict(context.Context, any) (tiny.Prediction, error) {
	return tiny.Prediction{}, nil
}
func (nopPredictor) Close() error { return nil }

type nopSink struct{}

func (nopSink) Store(context.Context, tiny.Ref, tiny.Prediction) error { return nil }
