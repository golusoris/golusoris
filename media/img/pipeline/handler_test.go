package pipeline_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/media/img"
	"github.com/golusoris/golusoris/media/img/pipeline"
	"github.com/golusoris/golusoris/storage"
)

// stubProcessor returns img.ErrCGORequired for every op (mirrors the no-libvips
// build). Used where the resize result is irrelevant (signing tests) or to
// assert the 415 path.
type stubProcessor struct{}

func (stubProcessor) Resize(context.Context, []byte, int, int, img.ResizeOptions) ([]byte, error) {
	return nil, img.ErrCGORequired
}

func (stubProcessor) Convert(context.Context, []byte, img.Format, int) ([]byte, error) {
	return nil, img.ErrCGORequired
}

func (stubProcessor) Optimize(context.Context, []byte) ([]byte, error) {
	return nil, img.ErrCGORequired
}

func (stubProcessor) Info(context.Context, []byte) (int, int, img.Format, error) {
	return 0, 0, "", img.ErrCGORequired
}
func (stubProcessor) Close() {}

// fakeProcessor echoes a deterministic body so the handler's 200 path is
// exercisable without CGO. It records the last resize box for assertions.
type fakeProcessor struct {
	lastW, lastH int
}

func (f *fakeProcessor) Resize(_ context.Context, _ []byte, w, h int, _ img.ResizeOptions) ([]byte, error) {
	f.lastW, f.lastH = w, h
	return []byte("resized-body"), nil
}

func (f *fakeProcessor) Convert(_ context.Context, src []byte, _ img.Format, _ int) ([]byte, error) {
	return append([]byte("converted-"), src...), nil
}
func (f *fakeProcessor) Optimize(_ context.Context, src []byte) ([]byte, error) { return src, nil }

func (f *fakeProcessor) Info(context.Context, []byte) (int, int, img.Format, error) {
	return 0, 0, img.FormatJPEG, nil
}
func (f *fakeProcessor) Close() {}

// mapSource is an in-memory storage.Source. A missing key returns
// storage.ErrNotFound so the handler maps it to 404.
type mapSource map[string][]byte

func (m mapSource) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := m[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func newHandlerPipeline(t *testing.T, proc img.Processor, src pipeline.Source) (*pipeline.Pipeline, *clockwork.FakeClock) {
	t.Helper()
	fc := clockwork.NewFakeClock()
	p, err := pipeline.New(pipeline.Options{Secret: testSecret}, proc, src, fc, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, fc
}

func TestHandler_200(t *testing.T) {
	t.Parallel()
	src := mapSource{"pics/cat.png": []byte("rawpng")}
	proc := &fakeProcessor{}
	p, _ := newHandlerPipeline(t, proc, src)

	tok, err := p.Sign("pics/cat.png", pipeline.Transform{Width: 200, Height: 100, Format: "webp"}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	rec := serve(t, p, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != pipeline.DefaultCacheControl {
		t.Errorf("Cache-Control = %q, want default", cc)
	}
	if proc.lastW != 200 || proc.lastH != 100 {
		t.Errorf("resize box = %dx%d, want 200x100", proc.lastW, proc.lastH)
	}
	if !strings.Contains(rec.Body.String(), "resized-body") {
		t.Errorf("body missing resized payload: %q", rec.Body.String())
	}
}

func TestHandler_400_malformedToken(t *testing.T) {
	t.Parallel()
	p, _ := newHandlerPipeline(t, &fakeProcessor{}, mapSource{})
	rec := serve(t, p, "not-a-valid-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_403_badSignature(t *testing.T) {
	t.Parallel()
	signer, _ := newHandlerPipeline(t, &fakeProcessor{}, mapSource{})
	tok, err := signer.Sign("pics/cat.png", pipeline.Transform{Width: 50}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Verify under a different secret => signature mismatch => 403.
	other, err := pipeline.New(
		pipeline.Options{Secret: "wrong-secret-aaaaaaaaaaaaaaaaaa"},
		&fakeProcessor{}, mapSource{}, clockwork.NewFakeClock(), slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := serve(t, other, tok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_403_expired(t *testing.T) {
	t.Parallel()
	p, fc := newHandlerPipeline(t, &fakeProcessor{}, mapSource{"k": []byte("x")})
	tok, err := p.Sign("k", pipeline.Transform{Width: 50}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	fc.Advance(2 * time.Minute)
	rec := serve(t, p, tok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandler_404_missingSource(t *testing.T) {
	t.Parallel()
	p, _ := newHandlerPipeline(t, &fakeProcessor{}, mapSource{}) // empty store
	tok, err := p.Sign("missing.png", pipeline.Transform{Width: 50, Format: "png"}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec := serve(t, p, tok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandler_415_noCGOBackend(t *testing.T) {
	t.Parallel()
	// stubProcessor mirrors a no-libvips build: resize => img.ErrCGORequired.
	p, _ := newHandlerPipeline(t, stubProcessor{}, mapSource{"k": []byte("raw")})
	tok, err := p.Sign("k", pipeline.Transform{Width: 50, Format: "png"}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec := serve(t, p, tok)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestHandler_HEAD_noBody(t *testing.T) {
	t.Parallel()
	p, _ := newHandlerPipeline(t, &fakeProcessor{}, mapSource{"k": []byte("raw")})
	tok, err := p.Sign("k", pipeline.Transform{Width: 50, Format: "png"}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodHead, "/img/"+tok, nil)
	req.SetPathValue("signed", tok)
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned body of %d bytes", rec.Body.Len())
	}
}

func TestHandler_tokenFromPathFallback(t *testing.T) {
	t.Parallel()
	// No SetPathValue: the handler must fall back to the last path segment.
	p, _ := newHandlerPipeline(t, &fakeProcessor{}, mapSource{"k": []byte("raw")})
	tok, err := p.Sign("k", pipeline.Transform{Width: 16, Format: "png"}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/img/"+tok, nil)
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fallback extraction); body=%s", rec.Code, rec.Body.String())
	}
}

// serve issues a GET against the handler with the token as the {signed} param.
func serve(t *testing.T, p *pipeline.Pipeline, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/img/"+token, nil)
	req.SetPathValue("signed", token)
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, req)
	return rec
}
