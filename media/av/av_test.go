package av_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/media/av"
)

func TestNewProber_stubReturnsError(t *testing.T) {
	t.Parallel()
	_, err := av.NewProber(av.Options{})
	if !errors.Is(err, av.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestNewTranscoder_stubReturnsError(t *testing.T) {
	t.Parallel()
	_, err := av.NewTranscoder(av.Options{})
	if !errors.Is(err, av.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestStub_Probe(t *testing.T) {
	t.Parallel()
	p, _ := av.NewProber(av.Options{})
	_, err := p.Probe(context.Background(), "video.mp4")
	if !errors.Is(err, av.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}
