package pipeline_test

import (
	"bytes"
	"context"
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

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
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

// TestModule_wiresPipelineAndHandler boots the fx Module against a config that
// supplies the signing secret, a LocalBucket seeded with one object, and a fake
// clock, then drives the provided handler end-to-end. This exercises
// loadOptions, newProcessor, newPipeline, and the bucketSource adapter.
func TestModule_wiresPipelineAndHandler(t *testing.T) {
	t.Parallel()

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
		storage.Module,
		pipeline.Module,
		fx.Populate(&p, &bkt),
		fx.Populate(fx.Annotate(&h, fx.ParamTags(`name:"media.img.pipeline"`))),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
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

	// The handler should reach the LocalBucket. With the no-CGO stub processor
	// the resize yields 415; with a real libvips build it yields 200. Either
	// proves the source fetch + routing path ran (not 404/403/400).
	tok, signErr := p.Sign("logo.png", pipeline.Transform{Width: 32, Format: "png"}, time.Minute)
	if signErr != nil {
		t.Fatalf("Sign: %v", signErr)
	}
	req := httptest.NewRequest(http.MethodGet, "/img/"+tok, nil)
	req.SetPathValue("signed", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	switch rec.Code {
	case http.StatusOK, http.StatusUnsupportedMediaType:
		// ok: source was fetched; resize either succeeded or hit the CGO stub.
	default:
		t.Fatalf("status = %d, want 200 or 415; body=%s", rec.Code, rec.Body.String())
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
