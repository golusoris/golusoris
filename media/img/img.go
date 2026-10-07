// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package img defines a backend-neutral image-processing contract.
//
// The repository does not ship a runtime image backend. Applications provide
// a [Processor] implementation and inject it into consumers such as
// media/img/pipeline. [NewProcessor] fails explicitly so an unavailable backend
// cannot be mistaken for a working optional build.
package img

import (
	"context"
	"errors"
)

// ErrCGORequired is returned because no runtime image backend is bundled.
// The name is retained for API compatibility.
var ErrCGORequired = errors.New("img: no runtime backend is bundled; inject a Processor")

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

// Options is retained for the compatibility [NewProcessor] constructor.
// It has no effect while the repository ships no runtime backend.
type Options struct {
	// Concurrency is reserved for application backends.
	Concurrency int
	// MaxCacheSize is reserved for application backends.
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

// NewProcessor returns [ErrCGORequired]. Applications must inject their own
// [Processor] implementation.
func NewProcessor(_ Options) (Processor, error) {
	return stub{}, ErrCGORequired
}
