package safety

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"

	"github.com/golusoris/golusoris/storage/safety/internal/exif"
)

// Stripping errors.
var (
	ErrUnsupportedType = errors.New("storage/safety: unsupported media type")
	ErrImageTooLarge   = errors.New("storage/safety: image exceeds max pixels")
)

// Stripper removes metadata from raster images by re-encoding them.
type Stripper interface {
	// Strip decodes src, optionally bakes JPEG orientation into pixels, drops
	// ALL metadata, and writes a clean re-encoded image. detectedType is the
	// sniffed content type (image/jpeg, image/png, image/gif). It returns
	// ErrUnsupportedType for anything not safely re-encodable and
	// ErrImageTooLarge when declared dimensions exceed the configured cap.
	Strip(ctx context.Context, src io.Reader, detectedType string) (io.Reader, string, error)
}

type stripper struct {
	opts   StripOptions
	logger *slog.Logger
}

func newStripper(opts Options, logger *slog.Logger) Stripper {
	return &stripper{opts: opts.Strip, logger: logger}
}

// Strip implements [Stripper]. It buffers src once so DecodeConfig (the
// decode-bomb gate) and the EXIF orientation read run before a full Decode.
func (s *stripper) Strip(
	ctx context.Context,
	src io.Reader,
	detectedType string,
) (io.Reader, string, error) {
	if !supportedType(detectedType) {
		return nil, "", fmt.Errorf("%w: %q", ErrUnsupportedType, detectedType)
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		return nil, "", fmt.Errorf("storage/safety: read source: %w", err)
	}
	if err = s.gateDimensions(raw); err != nil {
		return nil, "", err
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("storage/safety: decode image: %w", err)
	}
	if s.opts.AutoOrient && format == "jpeg" {
		img = s.applyOrientation(ctx, img, raw)
	}
	out, outType, err := s.encode(img, format)
	if err != nil {
		return nil, "", err
	}
	return bytes.NewReader(out), outType, nil
}

// gateDimensions rejects decode bombs before a full decode using DecodeConfig.
func (s *stripper) gateDimensions(raw []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("storage/safety: decode config: %w", err)
	}
	if s.opts.MaxPixels > 0 && cfg.Width*cfg.Height > s.opts.MaxPixels {
		return fmt.Errorf("%w: %d > %d", ErrImageTooLarge, cfg.Width*cfg.Height, s.opts.MaxPixels)
	}
	return nil
}

// applyOrientation bakes the JPEG EXIF Orientation tag into pixels so the
// stripped output displays upright. A missing or unreadable tag is a no-op.
func (s *stripper) applyOrientation(ctx context.Context, img image.Image, raw []byte) image.Image {
	o, err := exif.Orientation(raw)
	if err != nil {
		s.logger.DebugContext(ctx, "storage/safety: orientation read failed", slog.Any("error", err))
		return img
	}
	return exif.Apply(img, o)
}

// encode re-encodes img in its original format, dropping all metadata.
func (s *stripper) encode(img image.Image, format string) ([]byte, string, error) {
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: s.opts.JPEGQuality}); err != nil {
			return nil, "", fmt.Errorf("storage/safety: encode jpeg: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil
	case "png":
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("storage/safety: encode png: %w", err)
		}
		return buf.Bytes(), "image/png", nil
	case "gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			return nil, "", fmt.Errorf("storage/safety: encode gif: %w", err)
		}
		return buf.Bytes(), "image/gif", nil
	default:
		return nil, "", fmt.Errorf("%w: %q", ErrUnsupportedType, format)
	}
}

// supportedType reports whether detectedType is a raster format this package
// can safely re-encode. Deny-by-default: SVG/PDF/Office are never "stripped".
func supportedType(detectedType string) bool {
	switch detectedType {
	case "image/jpeg", "image/jpg", "image/png", "image/gif":
		return true
	default:
		return false
	}
}
