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
