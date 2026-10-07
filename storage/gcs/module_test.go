// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/storage/gcs"
)

func configFrom(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newApp(t *testing.T, cfg *config.Config, got *storage.Bucket) *fxtest.App {
	t.Helper()
	return fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		gcs.Module,
		fx.Populate(got),
	)
}

func TestModule_ProvidesEmulatorBucket(t *testing.T) {
	t.Parallel()
	var got storage.Bucket
	app := newApp(t, configFrom(t,
		"storage:\n  gcs:\n    bucket: media\n    endpoint: http://127.0.0.1:1/storage/v1/\n"), &got)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.RequireStart()
	if _, ok := got.(*gcs.Bucket); !ok {
		t.Fatalf("provided %T, want *gcs.Bucket", got)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestModule_RejectsMissingBucket(t *testing.T) {
	t.Parallel()
	var got storage.Bucket
	app := fx.New(
		fx.Provide(func() *config.Config {
			return configFrom(t, "storage:\n  gcs:\n    endpoint: http://127.0.0.1:1/storage/v1/\n")
		}),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		gcs.Module,
		fx.Populate(&got),
		fx.NopLogger,
	)
	if app.Err() == nil {
		t.Fatal("module accepted config without bucket")
	}
}
