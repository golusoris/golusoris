package tus

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/storage"
)

// newTestParams builds newHandler params over a real local bucket + lifecycle.
func newTestParams(t *testing.T, opts Options) (params, *fxtest.Lifecycle) {
	t.Helper()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	if opts.ScratchDir == "" {
		opts.ScratchDir = t.TempDir()
	}
	lc := fxtest.NewLifecycle(t)
	return params{
		LC:     lc,
		Opts:   opts,
		Bucket: bucket,
		Logger: slog.New(slog.DiscardHandler),
		Clock:  clock.NewFake(),
	}, lc
}

// TestNewHandler_LifecycleStartStop boots the handler via newHandler and drives
// the fx lifecycle so the drain goroutine starts and stops cleanly.
func TestNewHandler_LifecycleStartStop(t *testing.T) {
	t.Parallel()
	p, lc := newTestParams(t, defaultOptions())
	h, err := newHandler(p)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	ctx := context.Background()
	if err = lc.Start(ctx); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	if h.drainCancel == nil {
		t.Fatal("drain not started")
	}
	stopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = lc.Stop(stopCtx); err != nil {
		t.Fatalf("lifecycle stop: %v", err)
	}
	select {
	case <-h.drainDone:
	default:
		t.Fatal("drain goroutine did not exit after Stop")
	}
}

// TestNewHandler_DisabledLogsButBuilds covers the disabled opt-in branch.
func TestNewHandler_DisabledLogsButBuilds(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	opts.Enabled = false
	p, lc := newTestParams(t, opts)
	h, err := newHandler(p)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	if h == nil {
		t.Fatal("handler should build even when disabled")
	}
	_ = lc
}

// TestNewHandler_BadScratchDir surfaces a constructor error when the scratch
// root cannot be created (a file occupies the path).
func TestNewHandler_BadScratchDir(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	opts := defaultOptions()
	opts.ScratchDir = filepath.Join(file, "child") // parent is a file
	p, _ := newTestParams(t, opts)
	p.Opts = opts
	if _, err := newHandler(p); err == nil {
		t.Fatal("expected scratch-dir error")
	}
}

// TestLoadOptions_Defaults round-trips defaults through an empty config.
func TestLoadOptions_Defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.BasePath != "/files/" || opts.Enabled {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
}

// TestLoadOptions_Override reads a value from a YAML file.
func TestLoadOptions_Override(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	body := "storage:\n  tus:\n    enabled: true\n    base_path: /up/\n    max_size: 1048576\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if !opts.Enabled || opts.BasePath != "/up/" || opts.MaxSize != 1048576 {
		t.Fatalf("override not applied: %+v", opts)
	}
}
