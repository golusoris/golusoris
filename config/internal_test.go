package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParserFor_yaml(t *testing.T) {
	t.Parallel()
	if parserFor("config.yaml") == nil {
		t.Error("expected non-nil parser for .yaml")
	}
	if parserFor("config.yml") == nil {
		t.Error("expected non-nil parser for .yml")
	}
}

func TestParserFor_json(t *testing.T) {
	t.Parallel()
	if parserFor("config.json") == nil {
		t.Error("expected non-nil parser for .json")
	}
}

func TestParserFor_unknown(t *testing.T) {
	t.Parallel()
	if parserFor("config.toml") != nil {
		t.Error("expected nil parser for .toml")
	}
}

func TestFire_invokesListeners(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte("x: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Files: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	var called int
	c.OnChange(func() { called++ })
	c.fire()
	c.fire()
	if called != 2 {
		t.Errorf("fire called listeners %d times, want 2", called)
	}
}
