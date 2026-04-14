//go:build ignore
// +build ignore

// Activate: remove the go:build ignore line above, then:
//   go get github.com/davidbyttow/govips/v2
//   go mod tidy

package img

import (
	"context"
	"fmt"
	"runtime"

	"github.com/davidbyttow/govips/v2/vips"
)

func wrapf(format string, a ...any) error { return fmt.Errorf("img: "+format, a...) }

type vipsProcessor struct{}

// NewProcessor returns a libvips-backed Processor.
func NewProcessor(opts Options) (Processor, error) {
	concurrency := opts.Concurrency
	if concurrency == 0 {
		concurrency = runtime.NumCPU()
	}
	vips.LoggingSettings(nil, vips.LogLevelWarning)
	vips.Startup(&vips.Config{ConcurrencyLevel: concurrency, MaxCacheSize: opts.MaxCacheSize})
	return &vipsProcessor{}, nil
}

func (p *vipsProcessor) Resize(_ context.Context, src []byte, width, height int, opts ResizeOptions) ([]byte, error) {
	img, err := vips.NewImageFromBuffer(src)
	if err != nil {
		return nil, wrapf("load: %w", err)
	}
	defer img.Close()

	scale := min(float64(width)/float64(img.Width()), float64(height)/float64(img.Height()))
	if err := img.Resize(scale, vips.KernelAuto); err != nil {
		return nil, wrapf("resize: %w", err)
	}

	quality := opts.Quality
	if quality == 0 {
		quality = 85
	}
	ep := vips.NewDefaultExportParams()
	ep.Quality = quality
	if opts.StripMetadata {
		ep.StripMetadata = true
	}
	out, _, err := img.Export(ep)
	if err != nil {
		return nil, wrapf("export: %w", err)
	}
	return out, nil
}

func (p *vipsProcessor) Convert(_ context.Context, src []byte, format Format, quality int) ([]byte, error) {
	img, err := vips.NewImageFromBuffer(src)
	if err != nil {
		return nil, wrapf("load: %w", err)
	}
	defer img.Close()

	if quality == 0 {
		quality = 85
	}
	ep := vips.NewDefaultExportParams()
	ep.Quality = quality
	switch format {
	case FormatWEBP:
		ep.Format = vips.ImageTypeWEBP
	case FormatAVIF:
		ep.Format = vips.ImageTypeAVIF
	case FormatPNG:
		ep.Format = vips.ImageTypePNG
	case FormatJPEG:
		ep.Format = vips.ImageTypeJPEG
	}
	out, _, err := img.Export(ep)
	if err != nil {
		return nil, wrapf("export: %w", err)
	}
	return out, nil
}

func (p *vipsProcessor) Optimize(_ context.Context, src []byte) ([]byte, error) {
	img, err := vips.NewImageFromBuffer(src)
	if err != nil {
		return nil, wrapf("load: %w", err)
	}
	defer img.Close()
	ep := vips.NewDefaultExportParams()
	ep.StripMetadata = true
	out, _, err := img.Export(ep)
	if err != nil {
		return nil, wrapf("export: %w", err)
	}
	return out, nil
}

func (p *vipsProcessor) Info(_ context.Context, src []byte) (int, int, Format, error) {
	img, err := vips.NewImageFromBuffer(src)
	if err != nil {
		return 0, 0, "", wrapf("load: %w", err)
	}
	defer img.Close()
	return img.Width(), img.Height(), Format(img.Format().FileExt()), nil
}

func (p *vipsProcessor) Close() { vips.Shutdown() }
