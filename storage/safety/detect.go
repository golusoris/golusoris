// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety

import (
	"bytes"
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
	// ErrDeclaredTypeInvalid is returned by CheckDeclaredType, wrapped in
	// ErrTypeMismatch, when the declared content type fails mime.ParseMediaType
	// (e.g. a trailing "; charset" parameter with no value). An unparseable
	// declaration cannot be trusted to carry the type it claims, so it is
	// treated as an explicit mismatch rather than compared as a raw string.
	ErrDeclaredTypeInvalid = errors.New("storage/safety: declared type is not a valid media type")
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

// lenReader is satisfied by *bytes.Reader, *strings.Reader, and *bytes.Buffer:
// each reports its remaining unread length without consuming it. Detect uses
// it only to size an allocation hint; a Reader that does not implement it
// (e.g. an http.Response.Body) simply gets no hint and the bound-sized one.
type lenReader interface {
	Len() int
}

// Detect reads up to maxHeaderBytes from r (defaultHeaderBytes when
// maxHeaderBytes <= 0) and sniffs its magic-byte signature. A short read
// (r yields fewer bytes than the bound, including zero) is not itself an
// error; Detect sniffs whatever was read. It returns ErrEmptyInput only when
// r yields no bytes at all.
//
// The read is always hard-capped at maxHeaderBytes via io.LimitReader, so an
// arbitrary, possibly attacker-controlled Reader is never drained past the
// scalar bound (HISS-02) regardless of how large it actually is. The
// destination buffer itself, though, is grown only to the smaller of that
// bound and r's own remaining length when r exposes it (via lenReader) —
// e.g. a *bytes.Reader wrapping a 12-byte fixture allocates 12 bytes, not the
// full 8192-byte bound. A Reader with no known length (the common case, a
// network body) still allocates up to the bound, same as before.
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
	hint := maxHeaderBytes
	if lr, ok := r.(lenReader); ok {
		if n := lr.Len(); n >= 0 && n < hint {
			hint = n
		}
	}
	var buf bytes.Buffer
	buf.Grow(hint)
	if _, err := io.Copy(&buf, io.LimitReader(r, int64(maxHeaderBytes))); err != nil {
		return Detection{}, fmt.Errorf("storage/safety: read header: %w", err)
	}
	return DetectBytes(buf.Bytes())
}

// DetectBytes sniffs the magic-byte signature of an already-read buffer. It
// returns ErrEmptyInput for a zero-length buf.
//
// The emptiness check is filetype.Match's own: per h2non/filetype v1.1.3's
// match.go, an empty buffer is Match's only error case, and every non-empty
// buffer of any content returns (kind, nil) — so a second, redundant
// len(buf) == 0 guard here would leave Match's error branch permanently
// unreachable (and untested, violating HISS-15). Relying on it directly
// keeps this branch real and exercised by the empty-buffer tests instead of
// being dead code duplicating a check the dependency already makes.
func DetectBytes(buf []byte) (Detection, error) {
	kind, err := filetype.Match(buf)
	if err != nil {
		return Detection{}, fmt.Errorf("%w: %w", ErrEmptyInput, err)
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
//
// Every kind DetectBytes hands this today comes from filetype.Match, which
// (per h2non/filetype v1.1.3's matchers.init) only ever returns types.Unknown
// or a kind registered in one of the seven family maps checked below — so the
// default case is unreachable through this package's exported API and is
// exercised directly instead, by TestCategoryOf_UnregisteredType. It stays
// real defensive code rather than dead code: filetype.AddMatcher is a public
// extension point that lets any importer of the shared filetype package
// register a matcher for a kind belonging to none of those seven maps, and
// this must degrade to CategoryUnknown for such a kind, not panic or
// misclassify it.
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
// they disagree. Parameters (e.g. "; charset=utf-8") are ignored, and
// comparison is case-insensitive (mime.ParseMediaType lowercases the parsed
// media type per RFC 2045).
//
// A declared type that fails mime.ParseMediaType (e.g. a trailing
// "; charset" parameter with no value) is an explicit mismatch, not a raw
// string comparison fallback: the returned error wraps both ErrTypeMismatch
// and ErrDeclaredTypeInvalid, since an unparseable declaration cannot be
// trusted to carry the type it claims.
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
		return fmt.Errorf("%w: %w: declared %q: %w", ErrTypeMismatch, ErrDeclaredTypeInvalid, declared, err)
	}
	if declaredMIME != got.MIME {
		return fmt.Errorf("%w: declared %q, sniffed %q", ErrTypeMismatch, declared, got.MIME)
	}
	return nil
}
