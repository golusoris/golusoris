package sentry

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestSentryHandler_Enabled(t *testing.T) {
	t.Parallel()
	h := newSentryHandler()
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Debug should not be enabled")
	}
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info should not be enabled")
	}
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Warn should be enabled")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Error should be enabled")
	}
}

func TestSentryHandler_WithAttrs(t *testing.T) {
	t.Parallel()
	h := newSentryHandler()
	h2 := h.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if h2 == nil {
		t.Error("WithAttrs returned nil")
	}
	// WithAttrs on the derived handler should chain.
	h3 := h2.WithAttrs([]slog.Attr{slog.String("k2", "v2")})
	if h3 == nil {
		t.Error("chained WithAttrs returned nil")
	}
}

func TestSentryHandler_WithGroup(t *testing.T) {
	t.Parallel()
	h := newSentryHandler()
	h2 := h.WithGroup("grp")
	if h2 == nil {
		t.Error("WithGroup returned nil")
	}
}

func TestSentryHandler_Handle_warn(t *testing.T) {
	t.Parallel()
	// Sentry SDK is not initialized; Handle should not panic.
	h := newSentryHandler()
	r := slog.NewLogLogger(slog.NewTextHandler(&bytes.Buffer{}, nil), slog.LevelWarn)
	_ = r
	rec := slog.Record{}
	rec.Level = slog.LevelWarn
	rec.Message = "test warn"
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Errorf("Handle warn: %v", err)
	}
}

func TestSentryHandler_Handle_error(t *testing.T) {
	t.Parallel()
	h := newSentryHandler()
	rec := slog.Record{}
	rec.Level = slog.LevelError
	rec.Message = "test error"
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Errorf("Handle error: %v", err)
	}
}

func TestFanoutHandler(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	f := &fanoutHandler{handlers: []slog.Handler{base}}

	if !f.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("fanout should be enabled for Info")
	}

	rec := slog.Record{}
	rec.Level = slog.LevelInfo
	rec.Message = "fanout test"
	if err := f.Handle(context.Background(), rec); err != nil {
		t.Errorf("fanout Handle: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected output in buffer")
	}

	f2 := f.WithAttrs([]slog.Attr{slog.String("a", "b")})
	if f2 == nil {
		t.Error("WithAttrs returned nil")
	}
	f3 := f.WithGroup("grp")
	if f3 == nil {
		t.Error("WithGroup returned nil")
	}
}

func TestFanoutHandler_noHandlers(t *testing.T) {
	t.Parallel()
	f := &fanoutHandler{}
	if f.Enabled(context.Background(), slog.LevelError) {
		t.Error("empty fanout should not be enabled")
	}
}
