// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/h2non/filetype/types"
)

// Detection errors.
var (
	// ErrEmptyInput is returned by Detect/DetectBytes when given zero bytes.
	ErrEmptyInput = errors.New("storage/safety: empty input")
	// ErrTypeMismatch is returned by CheckDeclaredType when a caller-declared
	// content type disagrees with the sniffed magic-byte signature.
	ErrTypeMismatch = errors.New("storage/safety: declared type does not match sniffed content")
)

// defaultHeaderBytes bounds how many bytes Detect reads from a Reader when
// maxHeaderBytes <= 0. It matches filetype.MatchReader's own buffer size —
// large enough for the msooxml/zip-central-directory signatures the library
// checks — while still being a fixed scalar bound (HISS-02).
const defaultHeaderBytes = 8192

// Category buckets a [Detection] by the h2non/filetype matcher family its
// sniffed type belongs to, so callers can branch on "is this an image"
// without importing github.com/h2non/filetype/matchers directly.
type Category string

// Detected categories. CategoryUnknown covers both "no signature matched" and
// a signature filetype does not file into one of its family maps.
//
// Category mirrors filetype's own grouping verbatim, including its one
// surprising choice: PDF is filed under its Archive map upstream, not
// Document. This package does not override that so detection stays
// byte-for-byte identical to the underlying library.
const (
	CategoryImage       Category = "image"
	CategoryVideo       Category = "video"
	CategoryAudio       Category = "audio"
	CategoryFont        Category = "font"
	CategoryArchive     Category = "archive"
	CategoryDocument    Category = "document"
	CategoryApplication Category = "application"
	CategoryUnknown     Category = "unknown"
)

// Detection is the typed outcome of magic-byte content-type detection.
type Detection struct {
	// MIME is the sniffed MIME type, e.g. "image/png"; "" when Matched is false.
	MIME string
	// Extension is the sniffed file extension, e.g. "png"; "" when Matched is false.
	Extension string
	// Category is the matcher family the sniffed type belongs to;
	// CategoryUnknown when Matched is false.
	Category Category
	// Matched reports whether any magic-byte signature matched. False means
	// the input is a genuinely unrecognized (or signature-less) type, not an
	// error — most text-based formats (JSON, plain text, SVG) carry no magic
	// number for filetype to sniff.
	Matched bool
}

// DetectOptions controls magic-byte content-type detection via Detect.
type DetectOptions struct {
	// MaxHeaderBytes bounds how many bytes Detect reads from a Reader to sniff
	// a type. Zero or negative uses defaultHeaderBytes: unlike CleanKey's
	// optional length cap, this bound is always enforced — an arbitrary,
	// possibly attacker-controlled Reader must never be drained without a
	// scalar limit (HISS-02).
	MaxHeaderBytes int `koanf:"max_header_bytes"`
}

// Detect reads up to maxHeaderBytes from r (defaultHeaderBytes when
// maxHeaderBytes <= 0) and sniffs its magic-byte signature. A short read
// (r yields fewer bytes than the bound, including zero) is not itself an
// error; Detect sniffs whatever was read. It returns ErrEmptyInput only when
// r yields no bytes at all.
//
// ctx is checked for cancellation before the read starts; propagating
// cancellation mid-read is the caller's responsibility via r itself (e.g. an
// http.Response.Body from a context-bound request), matching how [Stripper]
// and [Fetcher] accept ctx in this package.
func Detect(ctx context.Context, r io.Reader, maxHeaderBytes int) (Detection, error) {
	if err := ctx.Err(); err != nil {
		return Detection{}, err
	}
	if maxHeaderBytes <= 0 {
		maxHeaderBytes = defaultHeaderBytes
	}
	buf := make([]byte, maxHeaderBytes)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Detection{}, fmt.Errorf("storage/safety: read header: %w", err)
	}
	return DetectBytes(buf[:n])
}

// DetectBytes sniffs the magic-byte signature of an already-read buffer. It
// returns ErrEmptyInput for a zero-length buf.
func DetectBytes(buf []byte) (Detection, error) {
	if len(buf) == 0 {
		return Detection{}, ErrEmptyInput
	}
	kind, err := filetype.Match(buf)
	if err != nil {
		return Detection{}, fmt.Errorf("storage/safety: sniff: %w", err)
	}
	if kind == types.Unknown {
		return Detection{Category: CategoryUnknown}, nil
	}
	return Detection{
		MIME:      kind.MIME.Value,
		Extension: kind.Extension,
		Category:  categoryOf(kind),
		Matched:   true,
	}, nil
}

// categoryOf reports which filetype matcher family kind was registered under.
func categoryOf(kind types.Type) Category {
	switch {
	case inMap(matchers.Image, kind):
		return CategoryImage
	case inMap(matchers.Video, kind):
		return CategoryVideo
	case inMap(matchers.Audio, kind):
		return CategoryAudio
	case inMap(matchers.Font, kind):
		return CategoryFont
	case inMap(matchers.Archive, kind):
		return CategoryArchive
	case inMap(matchers.Document, kind):
		return CategoryDocument
	case inMap(matchers.Application, kind):
		return CategoryApplication
	default:
		return CategoryUnknown
	}
}

// inMap reports whether kind is a key of m.
func inMap(m matchers.Map, kind types.Type) bool {
	_, ok := m[kind]
	return ok
}

// CheckDeclaredType compares a caller-declared content type — an HTTP
// Content-Type header, client-supplied upload metadata, [Fetcher]'s returned
// contentType — against a sniffed Detection and reports ErrTypeMismatch when
// they disagree. Parameters (e.g. "; charset=utf-8") are ignored.
//
// It cannot flag a mismatch when either side carries no information: an empty
// declared type, or a Detection with Matched == false, returns nil rather
// than guessing.
func CheckDeclaredType(got Detection, declared string) error {
	declared = strings.TrimSpace(declared)
	if declared == "" || !got.Matched {
		return nil
	}
	declaredMIME, _, err := mime.ParseMediaType(declared)
	if err != nil {
		declaredMIME = strings.ToLower(declared)
	}
	if declaredMIME != got.MIME {
		return fmt.Errorf("%w: declared %q, sniffed %q", ErrTypeMismatch, declared, got.MIME)
	}
	return nil
}
