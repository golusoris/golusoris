// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pipeline

import (
	"crypto/hmac"

	"github.com/golusoris/golusoris/media/img"
)

// hmacEqual is a constant-time MAC comparison (wraps crypto/hmac.Equal). Kept as
// a named helper so the security-relevant call site reads clearly.
func hmacEqual(a, b []byte) bool { return hmac.Equal(a, b) }

// contentTypeFor maps an output format to its wire MIME type. Passthrough
// renders inspect the output first, so only unknown processor formats reach
// the generic fallback.
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
