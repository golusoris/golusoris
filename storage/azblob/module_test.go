// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob_test

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/storage/azblob"
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

func moduleOptions(cfg *config.Config, got *storage.Bucket) fx.Option {
	return fx.Options(
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		azblob.Module,
		fx.Populate(got),
	)
}

func TestModule_ProvidesSharedKeyBucket(t *testing.T) {
	t.Parallel()
	key := make([]byte, 64)
	_, _ = rand.Read(key)
	var got storage.Bucket
	app := fxtest.New(t, moduleOptions(configFrom(t, "storage:\n  azblob:\n"+
		"    service_url: http://127.0.0.1:1/acct/\n    container: media\n"+
		"    account_name: acct\n    account_key: "+base64.StdEncoding.EncodeToString(key)+"\n"), &got))
	app.RequireStart().RequireStop()
	if _, ok := got.(*azblob.Bucket); !ok {
		t.Fatalf("provided %T, want *azblob.Bucket", got)
	}
}

func TestModule_RejectsMissingContainer(t *testing.T) {
	t.Parallel()
	var got storage.Bucket
	app := fx.New(moduleOptions(configFrom(t, "storage:\n  azblob:\n    service_url: https://acct.blob.core.windows.net/\n"), &got), fx.NopLogger)
	if app.Err() == nil {
		t.Fatal("module accepted config without container")
	}
}
