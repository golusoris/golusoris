package safety_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/storage/safety"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// bootSafety boots the safety Module against cfg and populates the provided
// Stripper + Fetcher.
func bootSafety(t *testing.T, cfg *config.Config) (safety.Stripper, safety.Fetcher) {
	t.Helper()
	var (
		strip safety.Stripper
		fetch safety.Fetcher
	)
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		safety.Module,
		fx.Populate(&strip, &fetch),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})
	return strip, fetch
}

func TestModule_Boots(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	strip, fetch := bootSafety(t, cfg)
	if strip == nil {
		t.Fatal("expected non-nil Stripper")
	}
	if fetch == nil {
		t.Fatal("expected non-nil Fetcher")
	}
}

func TestModule_LoadsConfigOverrides(t *testing.T) {
	t.Parallel()
	body := "" +
		"storage:\n" +
		"  safety:\n" +
		"    fetch:\n" +
		"      allow_private: true\n" +
		"      timeout: 30s\n" +
		"      max_bytes: 100\n" +
		"    strip:\n" +
		"      jpeg_quality: 60\n"
	cfg, err := config.New(config.Options{Files: []string{writeConfig(t, body)}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	// allow_private=true means a loopback fetch is no longer blocked; the boot
	// itself exercises loadOptions wiring with overrides.
	strip, fetch := bootSafety(t, cfg)
	if strip == nil || fetch == nil {
		t.Fatal("expected both providers wired")
	}
}

// TestModule_BadConfigFailsStart drives loadOptions' error branch: max_bytes
// as a non-numeric string cannot unmarshal into int64, so fx.Start must fail.
func TestModule_BadConfigFailsStart(t *testing.T) {
	t.Parallel()
	body := "" +
		"storage:\n" +
		"  safety:\n" +
		"    fetch:\n" +
		"      max_bytes: not-a-number\n"
	cfg, err := config.New(config.Options{Files: []string{writeConfig(t, body)}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		safety.Module,
		fx.Invoke(func(safety.Stripper, safety.Fetcher) {}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = app.Start(ctx); err == nil {
		_ = app.Stop(ctx)
		t.Fatal("expected Start to fail on malformed config")
	}
}
