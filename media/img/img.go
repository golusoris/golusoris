// Package img provides image processing helpers backed by libvips via govips
// (CGO). libvips must be installed before this package can be used.
//
// Install system deps (Debian/Ubuntu):
//
//	apt-get install libvips-dev
//
// Install system deps (macOS):
//
//	brew install vips
//
// Activate implementation:
//
//	1. Remove //go:build ignore from media/img/impl_govips.go
//	2. Add the dep: go get github.com/davidbyttow/govips/v2
//	3. go mod tidy
//
// Usage:
//
//	p, err := img.NewProcessor(img.Options{})
//	defer p.Close()
//	out, err := p.Resize(ctx, src, 800, 600)
//	out, err = p.Convert(ctx, src, img.FormatWEBP)
package img

import (
	"context"
	"errors"
)

// ErrCGORequired is returned when the govips implementation is not activated.
var ErrCGORequired = errors.New("img: CGO implementation not activated; see package doc")

// Format is an image output format.
type Format string

// Supported output formats.
const (
	FormatJPEG Format = "jpeg"
	FormatPNG  Format = "png"
	FormatWEBP Format = "webp"
	FormatAVIF Format = "avif"
	FormatGIF  Format = "gif"
	FormatTIFF Format = "tiff"
)

// Options configures the image processor.
type Options struct {
	// Concurrency sets the number of libvips threads (default: runtime.NumCPU).
	Concurrency int
	// MaxCacheSize caps the libvips operation cache (MiB). 0 = libvips default.
	MaxCacheSize int
}

// ResizeOptions fine-tunes a resize operation.
type ResizeOptions struct {
	// Fit controls how the image is fitted into the target box.
	// "cover" (default) fills and crops; "contain" letterboxes; "fill" stretches.
	Fit string
	// Quality for lossy formats (1-100; default 85).
	Quality int
	// StripMetadata removes EXIF/IPTC metadata.
	StripMetadata bool
}

// Processor processes images.
type Processor interface {
	// Resize scales src to at most width × height, preserving aspect ratio.
	Resize(ctx context.Context, src []byte, width, height int, opts ResizeOptions) ([]byte, error)
	// Convert re-encodes src in the target format.
	Convert(ctx context.Context, src []byte, format Format, quality int) ([]byte, error)
	// Optimize reduces file size without visible quality loss (format-aware).
	Optimize(ctx context.Context, src []byte) ([]byte, error)
	// Info returns width, height, and format of src without full decode.
	Info(ctx context.Context, src []byte) (width, height int, format Format, err error)
	// Close releases libvips resources.
	Close()
}

// stub returns ErrCGORequired for every operation.
type stub struct{}

func (stub) Resize(_ context.Context, _ []byte, _, _ int, _ ResizeOptions) ([]byte, error) {
	return nil, ErrCGORequired
}
func (stub) Convert(_ context.Context, _ []byte, _ Format, _ int) ([]byte, error) {
	return nil, ErrCGORequired
}
func (stub) Optimize(_ context.Context, _ []byte) ([]byte, error) { return nil, ErrCGORequired }
func (stub) Info(_ context.Context, _ []byte) (int, int, Format, error) {
	return 0, 0, "", ErrCGORequired
}
func (stub) Close() {}

// NewProcessor returns a [Processor] backed by libvips.
// When the CGO implementation is not activated it returns an error.
func NewProcessor(_ Options) (Processor, error) {
	return stub{}, ErrCGORequired
}
