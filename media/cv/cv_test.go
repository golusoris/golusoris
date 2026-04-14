package cv_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/media/cv"
)

func TestNewAnalyzer_stubReturnsError(t *testing.T) {
	t.Parallel()
	_, err := cv.NewAnalyzer(cv.Options{})
	if !errors.Is(err, cv.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}

func TestStub_DetectFaces(t *testing.T) {
	t.Parallel()
	a, _ := cv.NewAnalyzer(cv.Options{})
	_, err := a.DetectFaces(context.Background(), []byte{})
	if !errors.Is(err, cv.ErrCGORequired) {
		t.Fatalf("expected ErrCGORequired, got %v", err)
	}
}
