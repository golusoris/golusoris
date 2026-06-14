package flags_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/flags"
)

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

func TestModule_DefaultBackendNoop(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig(t)

	var client *flags.Client
	var provider flags.Provider
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		flags.Module,
		fx.Populate(&client, &provider),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}()

	if got := provider.Metadata().Name; got != "noop" {
		t.Errorf("default backend: got %q, want %q", got, "noop")
	}
	// Noop always returns the caller's default.
	if got := client.Bool(ctx, "anything", true); !got {
		t.Error("noop client should return the default value (true)")
	}
	if got := client.String(ctx, "anything", "fallback"); got != "fallback" {
		t.Errorf("noop client: got %q, want %q", got, "fallback")
	}
}

func TestModule_MemoryBackend(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig(t)

	var provider flags.Provider
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		// Override Options to select the memory backend without touching config files.
		fx.Decorate(func(flags.Options) flags.Options { return flags.Options{Backend: "memory"} }),
		flags.Module,
		fx.Populate(&provider),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}()

	if got := provider.Metadata().Name; got != "memory" {
		t.Errorf("backend: got %q, want %q", got, "memory")
	}
}
