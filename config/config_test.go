package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/config"
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

func TestFileLoad(t *testing.T) {
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
