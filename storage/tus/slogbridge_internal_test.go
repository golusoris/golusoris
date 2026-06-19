package tus

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	xslog "golang.org/x/exp/slog"
)

// TestXslogBridge_ForwardsToStdlib drives an x/exp/slog record through the
// bridge and asserts the stdlib handler receives the message + attrs, including
// grouped attrs (the path tusd exercises when logging structured fields).
func TestXslogBridge_ForwardsToStdlib(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	std := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	xl := xslogLogger(std)

	xl.WithGroup("req").With(xslog.String("svc", "tus")).Info(
		"hello",
		xslog.Int("code", 204),
		xslog.Group("meta", xslog.String("id", "abc")),
	)

	out := buf.String()
	for _, want := range []string{"hello", "tus", "204", "abc", "req"} {
		if !strings.Contains(out, want) {
			t.Fatalf("bridged log missing %q: %s", want, out)
		}
	}
}

// TestXslogBridge_Enabled honours the underlying handler's level gate.
func TestXslogBridge_Enabled(t *testing.T) {
	t.Parallel()
	std := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b := &xslogBridge{h: std.Handler()}
	if b.Enabled(context.Background(), xslog.LevelInfo) {
		t.Fatal("info should be disabled below warn")
	}
	if !b.Enabled(context.Background(), xslog.LevelError) {
		t.Fatal("error should be enabled at warn")
	}
}
