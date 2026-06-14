package idempotency_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
	"github.com/golusoris/golusoris/idempotency"
)

// TestModule_DefaultsOnEmptyConfig asserts loadOptions returns the documented
// defaults when no idempotency.* keys are set.
func TestModule_DefaultsOnEmptyConfig(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var got idempotency.Config
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		idempotency.Module,
		fx.Invoke(func(c idempotency.Config) { got = c }),
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

	if got.Required {
		t.Errorf("Required = true, want false (default)")
	}
	if got.TTL != 24*time.Hour {
		t.Errorf("TTL = %v, want 24h (default)", got.TTL)
	}
	if got.Header != "Idempotency-Key" {
		t.Errorf("Header = %q, want %q (default)", got.Header, "Idempotency-Key")
	}
}

// TestModule_BuildsDefaultBackend boots the Module via fxtest and asserts the
// default in-memory Store and the configured middleware are constructed.
func TestModule_BuildsDefaultBackend(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var (
		store idempotency.Store
		mw    middleware.Middleware
	)
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		idempotency.Module,
		fx.Invoke(func(s idempotency.Store, m middleware.Middleware) {
			store = s
			mw = m
		}),
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

	if store == nil {
		t.Fatal("Store not provided")
	}
	if _, ok := store.(*idempotency.MemoryStore); !ok {
		t.Errorf("default Store = %T, want *idempotency.MemoryStore", store)
	}
	if mw == nil {
		t.Fatal("middleware.Middleware not provided")
	}
}
