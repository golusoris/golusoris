// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pipeline_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/storage"
	"github.com/golusoris/golusoris/media/img"
	"github.com/golusoris/golusoris/media/img/pipeline"
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

type memoryObject struct {
	body   []byte
	object storage.Object
}

type mapBucket struct {
	mu      sync.RWMutex
	objects map[string]memoryObject
}

func (b *mapBucket) Put(ctx context.Context, key string, src io.Reader, opts storage.PutOptions) (storage.Object, error) {
	if err := ctx.Err(); err != nil {
		return storage.Object{}, err
	}
	body, err := io.ReadAll(src)
	if err != nil {
		return storage.Object{}, fmt.Errorf("pipeline: test bucket put: %w", err)
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = storage.DefaultContentType
	}
	obj := storage.Object{Key: key, Size: int64(len(body)), ContentType: contentType, Metadata: maps.Clone(opts.Metadata)}
	b.mu.Lock()
	if b.objects == nil {
		b.objects = make(map[string]memoryObject)
	}
	b.objects[key] = memoryObject{body: append([]byte(nil), body...), object: obj}
	b.mu.Unlock()
	return cloneObject(obj), nil
}

func (b *mapBucket) Get(ctx context.Context, key string) (io.ReadCloser, storage.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, storage.Object{}, err
	}
	b.mu.RLock()
	entry, ok := b.objects[key]
	b.mu.RUnlock()
	if !ok {
		return nil, storage.Object{}, storage.ErrNotFound
	}
	body := append([]byte(nil), entry.body...)
	return io.NopCloser(bytes.NewReader(body)), cloneObject(entry.object), nil
}

func (b *mapBucket) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.objects, key)
	b.mu.Unlock()
	return nil
}

func (b *mapBucket) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.mu.RLock()
	_, ok := b.objects[key]
	b.mu.RUnlock()
	return ok, nil
}

func (b *mapBucket) Stat(ctx context.Context, key string) (storage.Object, error) {
	if err := ctx.Err(); err != nil {
		return storage.Object{}, err
	}
	b.mu.RLock()
	entry, ok := b.objects[key]
	b.mu.RUnlock()
	if !ok {
		return storage.Object{}, storage.ErrNotFound
	}
	return cloneObject(entry.object), nil
}

func (b *mapBucket) List(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query, empty, err := storage.NormalizeListOptions(opts)
	if err != nil || empty {
		return nil, err
	}
	b.mu.RLock()
	objects := make([]storage.Object, 0, len(b.objects))
	for key, entry := range b.objects {
		if strings.HasPrefix(key, query.Prefix) && key > query.StartAfter {
			objects = append(objects, cloneObject(entry.object))
		}
	}
	b.mu.RUnlock()
	sort.Slice(objects, func(left, right int) bool { return objects[left].Key < objects[right].Key })
	if len(objects) > query.Limit {
		objects = objects[:query.Limit]
	}
	return objects, nil
}

func (b *mapBucket) URL(_ context.Context, _ string) (string, error) {
	return "", errors.New("pipeline: test bucket URL unsupported")
}

func cloneObject(obj storage.Object) storage.Object {
	obj.Metadata = maps.Clone(obj.Metadata)
	return obj
}

func newMapBucket(objects map[string][]byte) *mapBucket {
	bucket := &mapBucket{objects: make(map[string]memoryObject, len(objects))}
	for key, body := range objects {
		content := append([]byte(nil), body...)
		obj := storage.Object{Key: key, Size: int64(len(content)), ContentType: storage.DefaultContentType}
		bucket.objects[key] = memoryObject{body: content, object: obj}
	}
	return bucket
}

// bootModule boots the fx Module against a config that supplies the signing
// secret, an in-memory bucket seeded with "logo.png", and a fake clock, and returns
// the populated pipeline, named handler, and bucket.
func bootModule(t *testing.T, ctx context.Context) (*pipeline.Pipeline, http.Handler, storage.Bucket) {
	t.Helper()
	cfgFile := writeConfig(t, "media:\n  img:\n    pipeline:\n      secret: "+testSecret+"\n")

	cfg, err := config.New(config.Options{Files: []string{cfgFile}, Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	fc := clockwork.NewFakeClock()
	bkt := newMapBucket(map[string][]byte{"logo.png": []byte("rawbytes")})

	var (
		p *pipeline.Pipeline
		h http.Handler
	)
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() clock.Clock { return fc }),
		fx.Provide(func() img.Processor { return stubProcessor{} }),
		fx.Provide(func() storage.Bucket { return bkt }),
		pipeline.Module,
		fx.Populate(&p),
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
	bucket := newMapBucket(map[string][]byte{"source.png": []byte("source-bytes")})

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
	var typedNilBucket *mapBucket
	if pipeline.SourceFromBucket(typedNilBucket) != nil {
		t.Fatal("SourceFromBucket(typed nil) must return nil")
	}
}

// TestModule_loadOptionsMissingSecret asserts the Module fails to start when the
// signing secret is absent — an app cannot accidentally boot an unauthenticated
// resize proxy.
func TestModule_loadOptionsMissingSecret(t *testing.T) {
	t.Parallel()
	cfgFile := writeConfig(t, "media:\n  img:\n    pipeline: {}\n")
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
		fx.Provide(func() storage.Bucket { return newMapBucket(map[string][]byte{}) }),
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
