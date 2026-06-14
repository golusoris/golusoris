package secrets

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
)

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.Backend != "env" {
		t.Errorf("default backend = %q, want %q", opts.Backend, "env")
	}
}

func TestNewSecret_defaultEnvBackend(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	s, err := newSecret(defaultOptions(), logger)
	if err != nil {
		t.Fatalf("newSecret: %v", err)
	}
	if _, ok := s.(envStore); !ok {
		t.Fatalf("default backend = %T, want envStore", s)
	}
}

func TestNewSecret_fileBackend(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	s, err := newSecret(Options{Backend: "file", File: FileOptions{Dir: t.TempDir()}}, logger)
	if err != nil {
		t.Fatalf("newSecret: %v", err)
	}
	if _, ok := s.(fileStore); !ok {
		t.Fatalf("file backend = %T, want fileStore", s)
	}
}

func TestNewSecret_fileBackendMissingDir(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	if _, err := newSecret(Options{Backend: "file"}, logger); err == nil {
		t.Fatal("expected error for file backend without dir")
	}
}

func TestNewSecret_unknownBackend(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	if _, err := newSecret(Options{Backend: "vault"}, logger); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

// TestModule_StartsAndStops boots the secrets Module via fxtest to cover
// defaultOptions, loadOptions, and newSecret.
func TestModule_StartsAndStops(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		Module,
		fx.Invoke(func(Secret) {}),
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
