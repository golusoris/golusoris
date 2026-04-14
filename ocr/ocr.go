// Package ocr extracts text from images and PDFs using Tesseract via
// gosseract (CGO). The system library libtesseract + tessdata must be
// installed before this package can be used.
//
// Install system deps (Debian/Ubuntu):
//
//	apt-get install libtesseract-dev tesseract-ocr-eng libleptonica-dev
//
// Install system deps (macOS):
//
//	brew install tesseract
//
// Activate implementation:
//
//	1. Remove //go:build ignore from ocr/impl_gosseract.go
//	2. Add the dep: go get github.com/otiai10/gosseract/v2
//	3. go mod tidy
//
// Usage:
//
//	r, err := ocr.NewReader(ocr.Options{Language: "eng"})
//	text, err := r.Read(ctx, imageBytes)
package ocr

import (
	"context"
	"errors"
)

// ErrCGORequired is returned when the package is built without CGO or without
// the gosseract implementation activated.
var ErrCGORequired = errors.New("ocr: CGO implementation not activated; see package doc")

// Reader extracts text from image bytes (PNG, JPEG, TIFF, BMP, …).
type Reader interface {
	// Read returns the OCR text for the given image bytes.
	Read(ctx context.Context, src []byte) (string, error)
	// ReadFile returns the OCR text for the image at path.
	ReadFile(ctx context.Context, path string) (string, error)
	// Close releases Tesseract resources.
	Close() error
}

// Options configures the OCR reader.
type Options struct {
	// Language is the Tesseract language code (default: "eng").
	// Multiple languages: "eng+fra". Requires the tessdata files for each.
	Language string
	// TessdataPrefix overrides TESSDATA_PREFIX. Leave empty to use the env var.
	TessdataPrefix string
	// AllowList restricts recognized characters (e.g. "0123456789" for digits).
	AllowList string
}

// stub is the no-op implementation returned when the real one is not compiled in.
type stub struct{}

func (stub) Read(_ context.Context, _ []byte) (string, error)   { return "", ErrCGORequired }
func (stub) ReadFile(_ context.Context, _ string) (string, error) { return "", ErrCGORequired }
func (stub) Close() error                                         { return nil }

// NewReader returns a [Reader] backed by Tesseract.
// When the CGO implementation is not activated it returns an error.
func NewReader(_ Options) (Reader, error) {
	return stub{}, ErrCGORequired
}
