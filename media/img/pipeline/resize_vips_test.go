//go:build imgvips

// This integration test exercises a real libvips-backed resize through the
// pipeline. It is gated behind the `imgvips` build tag (mirroring media/img's
// activation pattern) and requires libvips + the govips dep:
//
//	go get github.com/davidbyttow/govips/v2
//	go test -tags imgvips -race ./pipeline/...
//
// Without the tag the package builds and tests with zero CGO; the signing,
// validation, and handler-routing logic above is fully covered there.
package pipeline_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/media/img"
	"github.com/golusoris/golusoris/media/img/pipeline"
)

// vipsProcessor is a minimal libvips-backed img.Processor for the integration
// test (the parent media/img processor ships stubbed until govips is activated).
type vipsProcessor struct{}

func (vipsProcessor) Resize(_ context.Context, src []byte, w, h int, opts img.ResizeOptions) ([]byte, error) {
	im, err := vips.NewImageFromBuffer(src)
	if err != nil {
		return nil, err
	}
	defer im.Close()
	scale := min(float64(w)/float64(im.Width()), float64(h)/float64(im.Height()))
	if rerr := im.Resize(scale, vips.KernelAuto); rerr != nil {
		return nil, rerr
	}
	ep := vips.NewDefaultExportParams()
	if opts.Quality > 0 {
		ep.Quality = opts.Quality
	}
	out, _, err := im.Export(ep)
	return out, err
}

func (vipsProcessor) Convert(_ context.Context, src []byte, format img.Format, q int) ([]byte, error) {
	im, err := vips.NewImageFromBuffer(src)
	if err != nil {
		return nil, err
	}
	defer im.Close()
	ep := vips.NewDefaultExportParams()
	if q > 0 {
		ep.Quality = q
	}
	switch format {
	case img.FormatPNG:
		ep.Format = vips.ImageTypePNG
	case img.FormatWEBP:
		ep.Format = vips.ImageTypeWEBP
	case img.FormatJPEG:
		ep.Format = vips.ImageTypeJPEG
	}
	out, _, err := im.Export(ep)
	return out, err
}
func (vipsProcessor) Optimize(_ context.Context, src []byte) ([]byte, error) { return src, nil }
func (vipsProcessor) Info(context.Context, []byte) (int, int, img.Format, error) {
	return 0, 0, img.FormatPNG, nil
}
func (vipsProcessor) Close() { vips.Shutdown() }

type pngSource struct{ data []byte }

func (s pngSource) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.Set(x, y, color.RGBA{uint8(x), uint8(y), 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestHandler_realResize(t *testing.T) {
	vips.Startup(nil)
	src := pngSource{data: makePNG(t, 800, 600)}
	p, err := pipeline.New(
		pipeline.Options{Secret: testSecret},
		vipsProcessor{}, src, clockwork.NewFakeClock(), slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tok, err := p.Sign("pic.png", pipeline.Transform{Width: 200, Height: 150, Format: "webp"}, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/img/"+tok, nil)
	req.SetPathValue("signed", tok)
	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("empty resized body")
	}
}
