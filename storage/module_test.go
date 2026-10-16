// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/testutil/configfile"
)

// bootBucket boots the storage Module against cfg and returns the provided
// Bucket. The fx app is stopped via t.Cleanup.
func bootBucket(t *testing.T, cfg *config.Config) storage.Bucket {
	t.Helper()
	var got storage.Bucket
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		storage.Module,
		fx.Populate(&got),
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
	return got
}

// TestModule_DefaultBackendIsLocal boots the Module with a config that only
// overrides the local path (kept inside a temp dir for hermeticity), leaving
// the backend selector unset. The default-backend fallback must yield a
// *LocalBucket.
func TestModule_DefaultBackendIsLocal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := config.New(config.Options{
		Files: []string{configfile.Write(t, "storage:\n  local:\n    path: "+dir+"\n")},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	got := bootBucket(t, cfg)
	if got == nil {
		t.Fatal("expected a non-nil storage.Bucket")
	}
	if _, ok := got.(*storage.LocalBucket); !ok {
		t.Errorf("expected *storage.LocalBucket, got %T", got)
	}
}

// TestModule_DefaultsOnEmptyConfig confirms an empty config drives the
// documented defaults (local backend, "./data"). The constructed bucket must
// be a *LocalBucket. t.Chdir isolates the created ./data inside a temp dir.
//
//nolint:paralleltest // t.Chdir forbids t.Parallel; the test mutates the working dir.
func TestModule_DefaultsOnEmptyConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	got := bootBucket(t, cfg)
	if _, ok := got.(*storage.LocalBucket); !ok {
		t.Errorf("expected default *storage.LocalBucket, got %T", got)
	}
}
