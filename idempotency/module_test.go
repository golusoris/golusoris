// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/httpx/middleware"
	"github.com/golusoris/golusoris/idempotency"
	"github.com/golusoris/golusoris/testutil/fxtest"
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
	fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		idempotency.Module,
		fx.Invoke(func(c idempotency.Config) { got = c }),
	)

	if got.Required {
		t.Errorf("Required = true, want false (default)")
	}
	if got.TTL != 24*time.Hour {
		t.Errorf("TTL = %v, want 24h (default)", got.TTL)
	}
	if got.Header != "Idempotency-Key" {
		t.Errorf("Header = %q, want %q (default)", got.Header, "Idempotency-Key")
	}
	if got.MaxRequestBody != 1<<20 || got.MaxResponseBody != 1<<20 {
		t.Errorf(
			"body limits = (%d, %d), want 1 MiB each",
			got.MaxRequestBody,
			got.MaxResponseBody,
		)
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
	fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		idempotency.Module,
		fx.Invoke(func(s idempotency.Store, m middleware.Middleware) {
			store = s
			mw = m
		}),
	)

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
