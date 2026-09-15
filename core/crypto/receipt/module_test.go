// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package receipt_test

import (
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/crypto/receipt"
)

func cfgFromYAML(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

func TestModuleUsesConfiguredSeed(t *testing.T) {
	t.Parallel()
	cfg := cfgFromYAML(t, "crypto:\n  receipt:\n    seed: "+hex.EncodeToString(fixedSeed)+"\n")
	var s *receipt.Signer
	app := fxtest.New(
		t,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		receipt.Module,
		fx.Populate(&s),
	)
	app.RequireStart().RequireStop()
	want, err := receipt.NewSignerFromSeed(fixedSeed, clock.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	if s.PublicKey() != want.PublicKey() {
		t.Fatal("module did not derive the signer from the configured seed")
	}
}

func TestModuleFallsBackToEphemeralKey(t *testing.T) {
	t.Parallel()
	cfg := cfgFromYAML(t, "app:\n  name: x\n")
	var s *receipt.Signer
	app := fxtest.New(
		t,
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		receipt.Module,
		fx.Populate(&s),
	)
	app.RequireStart().RequireStop()
	if s == nil || s.PublicKey() == "" {
		t.Fatal("expected an ephemeral signer")
	}
}

func TestModuleRejectsBadSeed(t *testing.T) {
	t.Parallel()
	cfg := cfgFromYAML(t, "crypto:\n  receipt:\n    seed: not-hex\n")
	var s *receipt.Signer
	app := fx.New(
		fx.Supply(cfg, slog.New(slog.DiscardHandler)),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		receipt.Module,
		fx.Populate(&s),
		fx.NopLogger,
	)
	if err := app.Err(); err == nil {
		t.Fatal("expected fx construction error for malformed seed")
	}
}
