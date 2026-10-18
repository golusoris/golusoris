// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flags_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/flags"
	"github.com/golusoris/golusoris/testutil/fxtest"
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
	fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		flags.Module,
		fx.Populate(&client, &provider),
	)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

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
	fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		// Override Options to select the memory backend without touching config files.
		fx.Decorate(func(flags.Options) flags.Options { return flags.Options{Backend: "memory"} }),
		flags.Module,
		fx.Populate(&provider),
	)
	if got := provider.Metadata().Name; got != "memory" {
		t.Errorf("backend: got %q, want %q", got, "memory")
	}
}
