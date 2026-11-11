// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ocr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/ocr"
)

func TestNewReader_stubReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ocr.NewReader(ocr.Options{Language: "eng"})
	if !errors.Is(err, ocr.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestStub_Read(t *testing.T) {
	t.Parallel()
	r := newStubReader(t)
	_, err := r.Read(context.Background(), []byte("fake"))
	if !errors.Is(err, ocr.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestStub_ReadFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
	}{
		{name: "image", path: "image.png"},
		{name: "missing", path: "missing.png"},
		{name: "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newStubReader(t)
			text, err := r.ReadFile(t.Context(), tc.path)
			if !errors.Is(err, ocr.ErrCGORequired) {
				t.Fatalf("ReadFile() error = %v, want ErrCGORequired", err)
			}
			if text != "" {
				t.Fatalf("ReadFile() = %q, want empty text", text)
			}
		})
	}
}

func TestStub_Close(t *testing.T) {
	t.Parallel()
	r := newStubReader(t)
	if err := r.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
}

func newStubReader(t *testing.T) ocr.Reader {
	t.Helper()
	r, err := ocr.NewReader(ocr.Options{})
	if !errors.Is(err, ocr.ErrCGORequired) {
		t.Fatalf("NewReader() error = %v, want ErrCGORequired", err)
	}
	return r
}
