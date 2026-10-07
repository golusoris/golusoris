// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pipeline_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/media/img"
	"github.com/golusoris/golusoris/media/img/pipeline"
	"github.com/golusoris/golusoris/storage"
)

// writeConfig writes a YAML config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// bootModule boots the fx Module against a config that supplies the signing
// secret, a LocalBucket seeded with "logo.png", and a fake clock, and returns
// the populated pipeline, named handler, and bucket.
func bootModule(t *testing.T, ctx context.Context) (*pipeline.Pipeline, http.Handler, storage.Bucket) {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "logo.png"), []byte("rawbytes"), 0o600); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	cfgFile := writeConfig(t,
		"media:\n  img:\n    pipeline:\n      secret: "+testSecret+"\n"+
			"storage:\n  local:\n    path: "+dataDir+"\n")

	cfg, err := config.New(config.Options{Files: []string{cfgFile}, Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	fc := clockwork.NewFakeClock()

	var (
		p   *pipeline.Pipeline
		h   http.Handler
		bkt storage.Bucket
	)
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() clock.Clock { return fc }),
		fx.Provide(func() img.Processor { return stubProcessor{} }),
		storage.Module,
		pipeline.Module,
		fx.Populate(&p, &bkt),
		fx.Populate(fx.Annotate(&h, fx.ParamTags(`name:"media.img.pipeline"`))),
	)
	if startErr := app.Start(ctx); startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() {
		if stopErr := app.Stop(ctx); stopErr != nil {
			t.Fatalf("Stop: %v", stopErr)
		}
	})

	if p == nil || h == nil {
		t.Fatal("module did not provide pipeline + handler")
	}
	return p, h, bkt
}

// TestModule_wiresPipelineAndHandler boots the fx Module and drives the
// provided handler end-to-end. This exercises loadOptions, newPipeline, and
// the bucketSource adapter.
func TestModule_wiresPipelineAndHandler(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	p, h, bkt := bootModule(t, ctx)

	// The injected unavailable processor proves the source fetch and routing
	// path ran before the handler maps its error to 415.
	tok, signErr := p.Sign("logo.png", pipeline.Transform{Width: 32, Format: "png"}, time.Minute)
	if signErr != nil {
		t.Fatalf("Sign: %v", signErr)
	}
	req := httptest.NewRequest(http.MethodGet, "/img/"+tok, nil)
	req.SetPathValue("signed", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}

	// Sanity: the seeded object is reachable via the wired bucket.
	rc, _, getErr := bkt.Get(ctx, "logo.png")
	if getErr != nil {
		t.Fatalf("bucket.Get: %v", getErr)
	}
	defer func() { _ = rc.Close() }()
	var buf bytes.Buffer
	if _, copyErr := buf.ReadFrom(rc); copyErr != nil {
		t.Fatalf("read object: %v", copyErr)
	}
	if buf.String() != "rawbytes" {
		t.Errorf("object body = %q, want rawbytes", buf.String())
	}
}

func TestSourceFromBucket(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	if _, err = bucket.Put(ctx, "source.png", bytes.NewBufferString("source-bytes"), storage.PutOptions{}); err != nil {
		t.Fatalf("bucket.Put: %v", err)
	}

	source := pipeline.SourceFromBucket(bucket)
	if source == nil {
		t.Fatal("SourceFromBucket(valid) = nil")
	}
	rc, err := source.Get(ctx, "source.png")
	if err != nil {
		t.Fatalf("source.Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(got) != "source-bytes" {
		t.Fatalf("source body = %q, want source-bytes", got)
	}
	if pipeline.SourceFromBucket(nil) != nil {
		t.Fatal("SourceFromBucket(nil) must return nil")
	}
	var typedNilBucket *storage.LocalBucket
	if pipeline.SourceFromBucket(typedNilBucket) != nil {
		t.Fatal("SourceFromBucket(typed nil) must return nil")
	}
}

// TestModule_loadOptionsMissingSecret asserts the Module fails to start when the
// signing secret is absent — an app cannot accidentally boot an unauthenticated
// resize proxy.
func TestModule_loadOptionsMissingSecret(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	cfgFile := writeConfig(t, "storage:\n  local:\n    path: "+dataDir+"\n")
	cfg, err := config.New(config.Options{Files: []string{cfgFile}, Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var p *pipeline.Pipeline
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() clock.Clock { return clockwork.NewFakeClock() }),
		fx.Provide(func() img.Processor { return stubProcessor{} }),
		storage.Module,
		pipeline.Module,
		fx.Populate(&p),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if startErr := app.Start(ctx); startErr == nil {
		_ = app.Stop(ctx)
		t.Fatal("want Start error for missing secret, got nil")
	}
}
