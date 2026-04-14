package img_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/media/img"
)

func TestNewProcessor_stubReturnsError(t *testing.T) {
	t.Parallel()
	_, err := img.NewProcessor(img.Options{})
	if !errors.Is(err, img.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestStub_Resize(t *testing.T) {
	t.Parallel()
	p, _ := img.NewProcessor(img.Options{})
	_, err := p.Resize(context.Background(), []byte{}, 100, 100, img.ResizeOptions{})
	if !errors.Is(err, img.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}
