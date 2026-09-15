// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/storage/safety"
)

// Real magic-byte fixtures, verified against github.com/h2non/filetype's own
// matcher source (matchers/image.go, matchers/archive.go) rather than
// remembered signatures.
var (
	pngFixture  = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	jpegFixture = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}
	pdfFixture  = []byte("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n1 0 obj\n")
	zipFixture  = []byte{0x50, 0x4B, 0x03, 0x04, 0x14, 0x00, 0x00, 0x00, 0x00, 0x00}
	mp4Fixture  = []byte{0, 0, 0, 0, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	midiFixture = []byte{0x4D, 0x54, 0x68, 0x64, 0x00, 0x00, 0x00, 0x06}
	ttfFixture  = []byte{0x00, 0x01, 0x00, 0x00, 0x00}
	docFixture  = docFixtureBytes()
	wasmFixture = []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
)

// docFixtureBytes builds a >513-byte CFBF buffer discriminated as legacy
// .doc at bytes 512-513. Doc/Xls/Ppt share an identical <=513-byte prefix
// check in h2non/filetype (matchers/document.go), so a short buffer's
// classification among the three is genuinely ambiguous — resolved only by
// Go's randomized map-iteration order at the library's init(), i.e.
// nondeterministic per process. A discriminated buffer avoids that trap.
func docFixtureBytes() []byte {
	buf := make([]byte, 514)
	buf[0], buf[1], buf[2], buf[3] = 0xD0, 0xCF, 0x11, 0xE0
	buf[512], buf[513] = 0xEC, 0xA5
	return buf
}

func TestDetectBytes_KnownSignatures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		buf      []byte
		wantMIME string
		wantExt  string
		wantCat  safety.Category
	}{
		{"png", pngFixture, "image/png", "png", safety.CategoryImage},
		{"jpeg", jpegFixture, "image/jpeg", "jpg", safety.CategoryImage},
		// PDF is filed under filetype's own Archive matcher map upstream, not
		// Document; Category mirrors that verbatim (see detect.go doc comment).
		{"pdf", pdfFixture, "application/pdf", "pdf", safety.CategoryArchive},
		{"zip", zipFixture, "application/zip", "zip", safety.CategoryArchive},
		// One fixture per remaining Category, so categoryOf's full mapping
		// (not just Image/Archive) is exercised.
		{"mp4", mp4Fixture, "video/mp4", "mp4", safety.CategoryVideo},
		{"midi", midiFixture, "audio/midi", "mid", safety.CategoryAudio},
		{"ttf", ttfFixture, "application/font-sfnt", "ttf", safety.CategoryFont},
		{"doc", docFixture, "application/msword", "doc", safety.CategoryDocument},
		{"wasm", wasmFixture, "application/wasm", "wasm", safety.CategoryApplication},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := safety.DetectBytes(tt.buf)
			if err != nil {
				t.Fatalf("DetectBytes: %v", err)
			}
			if !got.Matched {
				t.Fatal("Matched = false, want true")
			}
			if got.MIME != tt.wantMIME {
				t.Errorf("MIME = %q, want %q", got.MIME, tt.wantMIME)
			}
			if got.Extension != tt.wantExt {
				t.Errorf("Extension = %q, want %q", got.Extension, tt.wantExt)
			}
			if got.Category != tt.wantCat {
				t.Errorf("Category = %q, want %q", got.Category, tt.wantCat)
			}
		})
	}
}

// TestDetect_KnownSignaturesViaReader exercises the Reader-bound path (not
// just DetectBytes) so the scalar read bound itself is covered for fixtures
// much shorter than defaultHeaderBytes.
func TestDetect_KnownSignaturesViaReader(t *testing.T) {
	t.Parallel()
	got, err := safety.Detect(context.Background(), bytes.NewReader(pngFixture), 0)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !got.Matched || got.MIME != "image/png" {
		t.Fatalf("Detect(png) = %+v, want matched image/png", got)
	}
}

func TestDetectBytes_UnknownBytes(t *testing.T) {
	t.Parallel()
	buf := bytes.Repeat([]byte{0xAB, 0xCD}, 16)
	got, err := safety.DetectBytes(buf)
	if err != nil {
		t.Fatalf("DetectBytes: %v", err)
	}
	if got.Matched {
		t.Fatalf("Matched = true for unknown bytes, got %+v", got)
	}
	if got.Category != safety.CategoryUnknown {
		t.Errorf("Category = %q, want %q", got.Category, safety.CategoryUnknown)
	}
	if got.MIME != "" || got.Extension != "" {
		t.Errorf("MIME/Extension should be empty for unknown bytes, got %+v", got)
	}
}

func TestDetectBytes_EmptyInput(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"nil", "zero-length"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf []byte
			if name == "zero-length" {
				buf = []byte{}
			}
			_, err := safety.DetectBytes(buf)
			if !errors.Is(err, safety.ErrEmptyInput) {
				t.Fatalf("err = %v, want ErrEmptyInput", err)
			}
		})
	}
}

func TestDetect_EmptyReader(t *testing.T) {
	t.Parallel()
	_, err := safety.Detect(context.Background(), bytes.NewReader(nil), 0)
	if !errors.Is(err, safety.ErrEmptyInput) {
		t.Fatalf("err = %v, want ErrEmptyInput", err)
	}
}

// TestDetect_TruncatedHeader_ShortReader is the boundary where the *source*
// runs out before the header bound: a reader shorter than maxHeaderBytes but
// carrying a complete, valid signature must still be sniffed correctly (the
// underlying io.ReadFull short read, io.ErrUnexpectedEOF, must not surface as
// an error).
func TestDetect_TruncatedHeader_ShortReader(t *testing.T) {
	t.Parallel()
	got, err := safety.Detect(context.Background(), bytes.NewReader(pngFixture), 8192)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !got.Matched || got.MIME != "image/png" {
		t.Fatalf("Detect = %+v, want matched image/png despite short read", got)
	}
}

// TestDetect_TruncatedHeader_BoundCutsSignature is the boundary where the
// scalar bound itself truncates an otherwise-complete, available signature:
// maxHeaderBytes=2 can never carry the 4-byte PNG magic, so detection must
// cleanly report no match rather than erroring or panicking on the short
// slice a matcher inspects.
func TestDetect_TruncatedHeader_BoundCutsSignature(t *testing.T) {
	t.Parallel()
	got, err := safety.Detect(context.Background(), bytes.NewReader(pngFixture), 2)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Matched {
		t.Fatalf("Matched = true with a 2-byte bound, want false: %+v", got)
	}
}

// TestDetect_ContextAlreadyCanceled proves Detect fails fast on an already
// canceled context without ever calling Read.
func TestDetect_ContextAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &panicOnReadReader{t: t}
	_, err := safety.Detect(ctx, r, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

type panicOnReadReader struct{ t *testing.T }

func (p *panicOnReadReader) Read([]byte) (int, error) {
	p.t.Helper()
	p.t.Fatal("Read called after context was already canceled")
	return 0, nil
}

func TestCheckDeclaredType(t *testing.T) {
	t.Parallel()
	png := safety.Detection{MIME: "image/png", Extension: "png", Category: safety.CategoryImage, Matched: true}
	unknown := safety.Detection{Category: safety.CategoryUnknown}

	tests := []struct {
		name     string
		got      safety.Detection
		declared string
		wantErr  bool
	}{
		{"matches", png, "image/png", false},
		{"matches with params", png, "image/png; charset=binary", false},
		{"mismatch", png, "application/pdf", true},
		{"empty declared is a no-op", png, "", false},
		{"unmatched detection is a no-op", unknown, "image/png", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := safety.CheckDeclaredType(tt.got, tt.declared)
			if tt.wantErr && !errors.Is(err, safety.ErrTypeMismatch) {
				t.Fatalf("err = %v, want ErrTypeMismatch", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}
