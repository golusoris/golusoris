// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	tusd "github.com/tus/tusd/v2/pkg/handler"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
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

func mustOnComplete(t *testing.T, h *Handler, id string, fn completionFn) {
	t.Helper()
	if err := h.OnComplete(id, fn); err != nil {
		t.Fatalf("OnComplete(%q): %v", id, err)
	}
}

func TestLocalScratch_EmptyRootCreatesOwnedIsolation(t *testing.T) {
	t.Parallel()
	first, err := newLocalScratch("")
	if err != nil {
		t.Fatalf("first default scratch: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := newLocalScratch("")
	if err != nil {
		t.Fatalf("second default scratch: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if first.root == second.root {
		t.Fatalf("default scratch roots collide at %q", first.root)
	}

	ctx := context.Background()
	if _, err = first.Create(ctx, tusd.FileInfo{ID: "shared", Size: 1}); err != nil {
		t.Fatalf("create first upload: %v", err)
	}
	if _, err = second.Get(ctx, "shared"); !errors.Is(err, tusd.ErrNotFound) {
		t.Fatalf("second scratch Get(shared) error = %v; want ErrNotFound", err)
	}
	if err = second.RemoveUpload(ctx, "shared"); err != nil {
		t.Fatalf("second scratch RemoveUpload(shared): %v", err)
	}
	if _, err = first.Get(ctx, "shared"); err != nil {
		t.Fatalf("second scratch removed first upload: %v", err)
	}

	secondRoot := second.root
	if err = second.Close(); err != nil {
		t.Fatalf("close second scratch: %v", err)
	}
	if _, err = os.Stat(secondRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned scratch root after Close = %v; want not exist", err)
	}
	if _, err = os.Stat(first.root); err != nil {
		t.Fatalf("closing second scratch affected first root: %v", err)
	}
}

func TestLocalScratch_CloseContextHonorsCancellation(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch("")
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch.root) })
	if _, err = scratch.Create(
		context.Background(), tusd.FileInfo{ID: "preserved", Size: 1},
	); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = scratch.CloseContext(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseContext error = %v; want context.Canceled", err)
	}
	if _, statErr := os.Stat(scratch.root); statErr != nil {
		t.Fatalf("owned scratch removed after canceled close: %v", statErr)
	}
}

func TestLocalScratch_CloseDoesNotRecursivelyDeleteUnexpectedTree(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch("")
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch.root) })
	unexpected := filepath.Join(scratch.root, "unexpected", "nested")
	if err = os.MkdirAll(unexpected, 0o700); err != nil {
		t.Fatalf("create unexpected tree: %v", err)
	}
	victim := filepath.Join(unexpected, "preserve")
	if err = os.WriteFile(victim, []byte("data"), 0o600); err != nil {
		t.Fatalf("seed unexpected tree: %v", err)
	}

	err = scratch.Close()

	if err == nil {
		t.Fatal("Close recursively removed unexpected scratch subtree")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("unexpected subtree changed: %v", statErr)
	}
}

// TestNewHandler_LifecycleStartStop boots the handler via newHandler and drives
// the fx lifecycle so the drain goroutine starts and stops cleanly.
func TestNewHandler_LifecycleStartStop(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	opts.Enabled = true
	p, lc := newTestParams(t, opts)
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

func TestNewHandler_RejectsNonPositiveSweepInterval(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	opts.Enabled = true
	opts.ExpirySweepInterval = 0
	p, _ := newTestParams(t, opts)
	if _, err := newHandler(p); err == nil {
		t.Fatal("expected non-positive expiry sweep interval error")
	}
}

func TestNewHandler_RejectsNonPositiveUploadExpiry(t *testing.T) {
	t.Parallel()
	for _, expiry := range []time.Duration{0, -time.Second} {
		opts := defaultOptions()
		opts.Enabled = true
		opts.UploadExpiry = expiry
		p, _ := newTestParams(t, opts)
		if _, err := newHandler(p); err == nil {
			t.Fatalf("UploadExpiry %v: expected error", expiry)
		}
	}
}

func TestNewHandler_RejectsUnboundedMaxSize(t *testing.T) {
	t.Parallel()
	for _, maxSize := range []int64{0, -1, math.MaxInt64} {
		opts := defaultOptions()
		opts.Enabled = true
		opts.MaxSize = maxSize
		p, _ := newTestParams(t, opts)
		if _, err := newHandler(p); err == nil {
			t.Fatalf("MaxSize %d: expected error", maxSize)
		}
	}
}

func TestNewHandler_RejectsUnsupportedScratchBackend(t *testing.T) {
	t.Parallel()
	for _, backend := range []string{"", "memory", "s3"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			opts := defaultOptions()
			opts.Enabled = true
			opts.Scratch = backend
			p, _ := newTestParams(t, opts)
			if _, err := newHandler(p); err == nil {
				t.Fatalf("Scratch %q: expected unsupported backend error", backend)
			}
		})
	}
}

// TestNewHandler_DisabledDoesNotExposeUploads covers the disabled opt-in branch.
func TestNewHandler_DisabledDoesNotExposeUploads(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	opts.Enabled = false
	opts.ScratchDir = filepath.Join(t.TempDir(), "scratch")
	p, lc := newTestParams(t, opts)
	p.Opts = opts
	h, err := newHandler(p)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	if h == nil {
		t.Fatal("handler should build even when disabled")
	}
	if _, statErr := os.Stat(opts.ScratchDir); !os.IsNotExist(statErr) {
		t.Fatalf("disabled handler created scratch directory: %v", statErr)
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, h.BasePath(), nil)
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled ServeHTTP status = %d, want 404", recorder.Code)
	}
	router := chi.NewRouter()
	h.Mount(router)
	mounted := httptest.NewRecorder()
	router.ServeHTTP(mounted, req)
	if mounted.Code != http.StatusNotFound {
		t.Fatalf("disabled Mount status = %d, want 404", mounted.Code)
	}
	if err = lc.Start(context.Background()); err != nil {
		t.Fatalf("disabled lifecycle start: %v", err)
	}
	if h.drainCancel != nil {
		t.Fatal("disabled handler started completion drain")
	}
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
	opts.Enabled = true
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
	if opts.BasePath != "/files/" || opts.ScratchDir != "" || opts.Enabled {
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
