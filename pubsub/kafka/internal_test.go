package kafka

import (
	"log/slog"
	"testing"
)

func TestNewSlogWriter(t *testing.T) {
	t.Parallel()
	w := newSlogWriter(slog.Default())
	if w == nil {
		t.Fatal("expected non-nil slogWriter")
	}
}

func TestSlogWriter_Write(t *testing.T) {
	t.Parallel()
	w := newSlogWriter(slog.Default())
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected n=5, got %d", n)
	}
}
