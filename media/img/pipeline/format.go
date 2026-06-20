package pipeline

import (
	"crypto/hmac"

	"github.com/golusoris/golusoris/media/img"
)

// hmacEqual is a constant-time MAC comparison (wraps crypto/hmac.Equal). Kept as
// a named helper so the security-relevant call site reads clearly.
func hmacEqual(a, b []byte) bool { return hmac.Equal(a, b) }

// contentTypeFor maps an output format to its wire MIME type. An empty format
// (source-format passthrough) maps to the generic octet-stream so the handler
// never lies about the body; callers should set a concrete format when they
// care about the header.
func contentTypeFor(f img.Format) string {
	switch f {
	case img.FormatJPEG:
		return "image/jpeg"
	case img.FormatPNG:
		return "image/png"
	case img.FormatWEBP:
		return "image/webp"
	case img.FormatAVIF:
		return "image/avif"
	case img.FormatGIF:
		return "image/gif"
	case img.FormatTIFF:
		return "image/tiff"
	default:
		return "application/octet-stream"
	}
}
