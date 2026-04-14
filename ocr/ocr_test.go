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
	r, _ := ocr.NewReader(ocr.Options{})
	_, err := r.Read(context.Background(), []byte("fake"))
	if !errors.Is(err, ocr.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}
