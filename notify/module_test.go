package notify_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/notify"
)

// TestLoadOptionsDefaults verifies an empty config yields the smtp default.
func TestLoadOptionsDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := notify.LoadOptionsForTest(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.Sender != "smtp" {
		t.Errorf("Sender = %q, want %q", opts.Sender, "smtp")
	}
}

// TestNewNotifierDefaultBackend covers the smtp default-sender path: with a
// host configured, the constructor builds a *Notifier without dialing (go-mail
// connects lazily per message, so this stays hermetic).
func TestNewNotifierDefaultBackend(t *testing.T) {
	t.Parallel()
	opts := notify.Options{Sender: "smtp", SMTP: notify.SMTPOptions{Host: "smtp.example.com"}}
	n, err := notify.NewNotifierForTest(opts, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newNotifier: %v", err)
	}
	if n == nil {
		t.Fatal("nil notifier")
	}
}

// TestNewNotifierUnknownSender verifies a non-built-in sender name is rejected.
func TestNewNotifierUnknownSender(t *testing.T) {
	t.Parallel()
	opts := notify.Options{Sender: "slack"}
	if _, err := notify.NewNotifierForTest(opts, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("expected error for unknown default sender")
	}
}

// TestModule_StartsAndStops boots the notify Module via fxtest to cover
// defaultOptions, loadOptions, and newNotifier end to end.
func TestModule_StartsAndStops(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("notify:\n  smtp:\n    host: smtp.example.com\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}, Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		notify.Module,
		fx.Invoke(func(*notify.Notifier) {}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
