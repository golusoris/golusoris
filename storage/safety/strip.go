// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	// ErrTooLarge when the encoded input exceeds MaxBytes. ErrImageTooLarge
	// reports declared dimensions or aggregate GIF frames above MaxPixels.
	Strip(ctx context.Context, src io.Reader, detectedType string) (io.Reader, string, error)
}

type stripper struct {
	opts   StripOptions
	logger *slog.Logger
}

func newStripper(opts Options, logger *slog.Logger) Stripper {
	logger = loggerOrDiscard(logger)
	if opts.Strip.MaxBytes <= 0 {
		opts.Strip.MaxBytes = defaultStripMaxBytes
	}
	if opts.Strip.MaxPixels <= 0 {
		opts.Strip.MaxPixels = defaultStripMaxPixels
	}
	return &stripper{opts: opts.Strip, logger: logger}
}

// Strip implements [Stripper]. It buffers src once so DecodeConfig (the
// decode-bomb gate) and the EXIF orientation read run before a full Decode.
func (s *stripper) Strip(
	ctx context.Context,
	src io.Reader,
	detectedType string,
) (io.Reader, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("storage/safety: strip preflight: %w", err)
	}
	if !supportedType(detectedType) {
		return nil, "", fmt.Errorf("%w: %q", ErrUnsupportedType, detectedType)
	}
	raw, err := readStripSource(ctx, src, s.opts.MaxBytes)
	if err != nil {
		return nil, "", err
	}
	if err = ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("storage/safety: strip after source read: %w", err)
	}
	format, err := s.gateDimensions(raw)
	if err != nil {
		return nil, "", err
	}
	if err = stripContextError(ctx, "after dimension gate"); err != nil {
		return nil, "", err
	}
	if format == "gif" {
		return s.stripGIF(ctx, raw)
	}
	return s.stripStatic(ctx, raw, format)
}

func (s *stripper) stripStatic(ctx context.Context, raw []byte, format string) (io.Reader, string, error) {
	img, decodedFormat, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("storage/safety: decode image: %w", err)
	}
	if decodedFormat != format {
		return nil, "", fmt.Errorf("storage/safety: decoded format changed from %q to %q", format, decodedFormat)
	}
	if err = stripContextError(ctx, "after image decode"); err != nil {
		return nil, "", err
	}
	if s.opts.AutoOrient && decodedFormat == "jpeg" {
		img = s.applyOrientation(ctx, img, raw)
		if err = stripContextError(ctx, "after image orientation"); err != nil {
			return nil, "", err
		}
	}
	out, outType, err := s.encode(img, decodedFormat)
	if err != nil {
		return nil, "", err
	}
	if err = stripContextError(ctx, "after image encode"); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(out), outType, nil
}

// gateDimensions rejects decode bombs before a full decode using DecodeConfig.
func (s *stripper) gateDimensions(raw []byte) (string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("storage/safety: decode config: %w", err)
	}
	if dimensionsExceed(cfg.Width, cfg.Height, s.opts.MaxPixels) {
		return "", fmt.Errorf("%w: %dx%d exceeds %d pixels", ErrImageTooLarge, cfg.Width, cfg.Height, s.opts.MaxPixels)
	}
	if format == "gif" {
		if err := gateGIFFrames(raw, s.opts.MaxPixels); err != nil {
			return "", err
		}
	}
	return format, nil
}

// stripGIF keeps animation timing while EncodeAll drops comment and text extensions.
func (s *stripper) stripGIF(ctx context.Context, raw []byte) (io.Reader, string, error) {
	animation, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("storage/safety: decode gif: %w", err)
	}
	if err = stripContextError(ctx, "after gif decode"); err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err = gif.EncodeAll(&buf, animation); err != nil {
		return nil, "", fmt.Errorf("storage/safety: encode gif: %w", err)
	}
	if err = stripContextError(ctx, "after gif encode"); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(buf.Bytes()), "image/gif", nil
}

func stripContextError(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage/safety: strip %s: %w", phase, err)
	}
	return nil
}

func gateGIFFrames(raw []byte, maxPixels int) error {
	offset, err := gifDataOffset(raw)
	if err != nil {
		return err
	}
	totalPixels, frames := 0, 0
	for range raw {
		if offset >= len(raw) {
			break
		}
		blockType := raw[offset]
		offset++
		switch blockType {
		case 0x21:
			offset, err = skipGIFExtension(raw, offset)
		case 0x2c:
			offset, totalPixels, err = scanGIFFrame(raw, offset, totalPixels, maxPixels)
			frames++
		case 0x3b:
			if frames == 0 {
				return errors.New("storage/safety: gif contains no frames")
			}
			return nil
		default:
			return fmt.Errorf("storage/safety: unsupported gif block 0x%02x", blockType)
		}
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("storage/safety: scan gif frames: %w", io.ErrUnexpectedEOF)
}

func gifDataOffset(raw []byte) (int, error) {
	if len(raw) < 13 || string(raw[:3]) != "GIF" {
		return 0, fmt.Errorf("storage/safety: scan gif header: %w", io.ErrUnexpectedEOF)
	}
	offset := 13
	if raw[10]&0x80 != 0 {
		offset += 3 * (1 << (uint(raw[10]&0x07) + 1))
	}
	if offset > len(raw) {
		return 0, fmt.Errorf("storage/safety: scan gif color table: %w", io.ErrUnexpectedEOF)
	}
	return offset, nil
}

func skipGIFExtension(raw []byte, offset int) (int, error) {
	if offset >= len(raw) {
		return 0, fmt.Errorf("storage/safety: scan gif extension: %w", io.ErrUnexpectedEOF)
	}
	return skipGIFSubBlocks(raw, offset+1)
}

func scanGIFFrame(raw []byte, offset, totalPixels, maxPixels int) (int, int, error) {
	const descriptorBytes = 9
	if offset > len(raw)-descriptorBytes {
		return 0, 0, fmt.Errorf("storage/safety: scan gif descriptor: %w", io.ErrUnexpectedEOF)
	}
	width := int(raw[offset+4]) | int(raw[offset+5])<<8
	height := int(raw[offset+6]) | int(raw[offset+7])<<8
	if dimensionsExceed(width, height, maxPixels-totalPixels) {
		return 0, 0, fmt.Errorf("%w: gif frames exceed %d total pixels", ErrImageTooLarge, maxPixels)
	}
	totalPixels += width * height
	packed := raw[offset+8]
	offset += descriptorBytes
	if packed&0x80 != 0 {
		offset += 3 * (1 << (uint(packed&0x07) + 1))
	}
	if offset >= len(raw) {
		return 0, 0, fmt.Errorf("storage/safety: scan gif image data: %w", io.ErrUnexpectedEOF)
	}
	returnOffset, err := skipGIFSubBlocks(raw, offset+1)
	return returnOffset, totalPixels, err
}

func skipGIFSubBlocks(raw []byte, offset int) (int, error) {
	for range raw {
		if offset >= len(raw) {
			return 0, fmt.Errorf("storage/safety: scan gif sub-block: %w", io.ErrUnexpectedEOF)
		}
		size := int(raw[offset])
		offset++
		if size == 0 {
			return offset, nil
		}
		if size > len(raw)-offset {
			return 0, fmt.Errorf("storage/safety: scan gif sub-block: %w", io.ErrUnexpectedEOF)
		}
		offset += size
	}
	return 0, errors.New("storage/safety: gif sub-block count exceeds input length")
}

func readStripSource(ctx context.Context, src io.Reader, maxBytes int64) ([]byte, error) {
	checked := stripCheckedReader{check: ctx.Err, src: src}
	limited := &io.LimitedReader{R: checked, N: maxBytes}
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("storage/safety: read source: %w", err)
	}
	if limited.N > 0 {
		return raw, nil
	}
	var sentinel [1]byte
	n, readErr := checked.Read(sentinel[:])
	if n > 0 {
		return nil, fmt.Errorf("%w: encoded image exceeds %d bytes", ErrTooLarge, maxBytes)
	}
	if errors.Is(readErr, io.EOF) {
		return raw, nil
	}
	if readErr != nil {
		return nil, fmt.Errorf("storage/safety: read source sentinel: %w", readErr)
	}
	return nil, fmt.Errorf("storage/safety: read source sentinel: %w", io.ErrNoProgress)
}

type stripCheckedReader struct {
	check func() error
	src   io.Reader
}

func (r stripCheckedReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, fmt.Errorf("storage/safety: source context: %w", err)
	}
	n, err := r.src.Read(p)
	if ctxErr := r.check(); ctxErr != nil {
		return n, fmt.Errorf("storage/safety: source context: %w", ctxErr)
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("storage/safety: read source: %w", err)
	}
	return n, nil
}

func dimensionsExceed(width, height, maxPixels int) bool {
	if maxPixels <= 0 {
		return true
	}
	if width <= 0 || height <= 0 {
		return true
	}
	return width > maxPixels/height
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
