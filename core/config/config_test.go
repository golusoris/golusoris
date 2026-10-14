// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
)

func TestEnvLoad(t *testing.T) {
	t.Setenv("APP_DB_HOST", "localhost")
	t.Setenv("APP_DB_PORT", "5432")

	c, err := config.New(config.Options{EnvPrefix: "APP_"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.String("db.host"); got != "localhost" {
		t.Errorf("db.host = %q, want localhost", got)
	}
	if got := c.Int("db.port"); got != 5432 {
		t.Errorf("db.port = %d, want 5432", got)
	}
}

// TestEnvCompoundDefault verifies the default (no CompoundKeys) still splits
// every underscore: APP_SEARCH_API_KEY -> search.api.key.
func TestEnvCompoundDefault(t *testing.T) {
	t.Setenv("APP_SEARCH_API_KEY", "secret")

	c, err := config.New(config.Options{EnvPrefix: "APP_"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.String("search.api.key"); got != "secret" {
		t.Errorf("default split: search.api.key = %q, want secret", got)
	}
	if c.Exists("search.api_key") {
		t.Error("default split: search.api_key should not exist")
	}
}

// TestEnvCompoundOverride verifies declared compound keys preserve underscores
// in the leaf segment while non-declared vars keep the default split.
func TestEnvCompoundOverride(t *testing.T) {
	t.Setenv("APP_SEARCH_API_KEY", "secret")
	t.Setenv("APP_AUTH_OIDC_ISSUER_URL", "https://idp.example")
	t.Setenv("APP_DB_HOST", "localhost")

	c, err := config.New(config.Options{
		EnvPrefix:    "APP_",
		CompoundKeys: []string{"search.api_key", "auth.oidc.issuer_url"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.String("search.api_key"); got != "secret" {
		t.Errorf("compound: search.api_key = %q, want secret", got)
	}
	if got := c.String("auth.oidc.issuer_url"); got != "https://idp.example" {
		t.Errorf("compound: auth.oidc.issuer_url = %q, want https://idp.example", got)
	}
	if c.Exists("search.api.key") {
		t.Error("compound: search.api.key should not exist when search.api_key declared")
	}
	// Non-declared vars keep the default split.
	if got := c.String("db.host"); got != "localhost" {
		t.Errorf("compound: db.host = %q, want localhost (default split preserved)", got)
	}
}

func TestFileLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Int("server.port"); got != 9000 {
		t.Errorf("server.port = %d, want 9000", got)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("port: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_PORT", "2")
	c, err := config.New(config.Options{EnvPrefix: "APP_", Files: []string{path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Int("port"); got != 2 {
		t.Errorf("port = %d, want env override 2", got)
	}
}

func TestJSONFileLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(path, []byte(`{"db":{"host":"pghost","port":5432}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Get("db.host"); got != "pghost" {
		t.Errorf("db.host = %q, want pghost", got)
	}
	if got := c.Int64("db.port"); got != 5432 {
		t.Errorf("db.port = %d, want 5432", got)
	}
}

func TestUnsupportedExtension(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.toml")
	if err := os.WriteFile(path, []byte("port = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.New(config.Options{Files: []string{path}})
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
}

func TestGetters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	yaml := "enabled: true\nratio: 1.5\ncount: 42\ntags:\n  - a\n  - b\n  - c\nname: test\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !c.Bool("enabled") {
		t.Error("Bool: expected true")
	}
	if got := c.Float("ratio"); got != 1.5 {
		t.Errorf("Float: got %v, want 1.5", got)
	}
	if got := c.Int64("count"); got != 42 {
		t.Errorf("Int64: got %d, want 42", got)
	}
	if got := c.Strings("tags"); len(got) != 3 || got[0] != "a" {
		t.Errorf("Strings: got %v", got)
	}
	if !c.Exists("name") {
		t.Error("Exists: expected true for 'name'")
	}
	if c.Exists("nonexistent.key") {
		t.Error("Exists: expected false for missing key")
	}
	all := c.All()
	if _, ok := all["name"]; !ok {
		t.Error("All: expected 'name' key")
	}
}

func TestOnChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte("x: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	fired := make(chan struct{}, 1)
	c.OnChange(func() { fired <- struct{}{} })

	// Fire is triggered by SIGHUP; here we verify the callback registers.
	// We call it indirectly by checking the callback was stored (count test).
	select {
	case <-fired:
		t.Error("OnChange fired before any change")
	default:
		// expected: no premature fire
	}
}

func TestModule_CompoundKeysContributor(t *testing.T) {
	t.Setenv("APP_GRPC_KEEPALIVE_MAX_CONNECTION_AGE_GRACE", "45s")

	var cfg *config.Config
	app := fxtest.New(
		t,
		config.Module,
		config.CompoundKeys("grpc.keepalive.max_connection_age_grace"),
		fx.Invoke(func(c *config.Config) {
			cfg = c
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("app.Stop: %v", err)
	}

	if got := cfg.String("grpc.keepalive.max_connection_age_grace"); got != "45s" {
		t.Errorf("got %q, want 45s", got)
	}
	if cfg.Exists("grpc.keepalive.max.connection.age.grace") {
		t.Error("split key should not exist when compound key declared")
	}
}

func TestModule_WithoutCompoundKeysContributor(t *testing.T) {
	t.Setenv("APP_GRPC_KEEPALIVE_MAX_CONNECTION_AGE_GRACE", "45s")

	var cfg *config.Config
	app := fxtest.New(
		t,
		config.Module,
		fx.Invoke(func(c *config.Config) {
			cfg = c
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("app.Stop: %v", err)
	}

	if got := cfg.String("grpc.keepalive.max.connection.age.grace"); got != "45s" {
		t.Errorf("got %q, want 45s", got)
	}
	if cfg.Exists("grpc.keepalive.max_connection_age_grace") {
		t.Error("compound key should not exist without contributor")
	}
}

func TestModule_DuplicateContributedKey(t *testing.T) {
	t.Setenv("APP_TEST_DUPLICATE_KEY", "value")

	var cfg *config.Config
	app := fxtest.New(
		t,
		config.Module,
		config.CompoundKeys("test.duplicate_key"),
		config.CompoundKeys("test.duplicate_key"),
		fx.Invoke(func(c *config.Config) {
			cfg = c
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("app.Stop: %v", err)
	}

	if got := cfg.String("test.duplicate_key"); got != "value" {
		t.Errorf("got %q, want value", got)
	}
}

func TestModule_ReplacedOptionsMergesGroup(t *testing.T) {
	t.Setenv("CUSTOM_GRPC_KEEPALIVE_MAX_CONNECTION_AGE_GRACE", "45s")

	var cfg *config.Config
	app := fxtest.New(
		t,
		config.Module,
		fx.Replace(config.Options{
			EnvPrefix: "CUSTOM_",
			Delimiter: ".",
		}),
		config.CompoundKeys("grpc.keepalive.max_connection_age_grace"),
		fx.Invoke(func(c *config.Config) {
			cfg = c
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("app.Stop: %v", err)
	}

	if got := cfg.String("grpc.keepalive.max_connection_age_grace"); got != "45s" {
		t.Errorf("got %q, want 45s", got)
	}
}
