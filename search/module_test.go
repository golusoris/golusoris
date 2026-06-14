package search

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
)

// writeConfig writes a YAML config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadOptions_DefaultsOnEmptyConfig confirms an empty config drives the
// documented default: the in-memory backend.
func TestLoadOptions_DefaultsOnEmptyConfig(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.Backend != backendMemory {
		t.Errorf("expected default backend %q, got %q", backendMemory, opts.Backend)
	}
}

// TestLoadOptions_ReadsConfig confirms the backend selector is read from config.
func TestLoadOptions_ReadsConfig(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{
		Files: []string{writeConfig(t, "search:\n  backend: memory\n")},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.Backend != backendMemory {
		t.Errorf("expected backend %q, got %q", backendMemory, opts.Backend)
	}
}

// TestNewBackend_DefaultIsMemory confirms the constructor builds a
// *MemorySearcher for the default backend.
func TestNewBackend_DefaultIsMemory(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	got, err := newBackend(defaultOptions(), logger)
	if err != nil {
		t.Fatalf("newBackend: %v", err)
	}
	if _, ok := got.(*MemorySearcher); !ok {
		t.Errorf("expected *MemorySearcher, got %T", got)
	}
}

// TestNewBackend_UnknownBackendErrors confirms an unknown selector is rejected.
func TestNewBackend_UnknownBackendErrors(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	if _, err := newBackend(Options{Backend: "nope"}, logger); err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
}

// TestNewBackend_ExternalBackendNotWired confirms selecting an external backend
// is an explicit error (those are app-wired, not Module-wired).
func TestNewBackend_ExternalBackendNotWired(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	for _, b := range []string{backendTypesense, backendMeilisearch, backendPgFTS} {
		if _, err := newBackend(Options{Backend: b}, logger); err == nil {
			t.Errorf("expected error for unwired backend %q, got nil", b)
		}
	}
}

// TestModule_ProvidesMemoryBackend boots the Module against an empty config and
// confirms it provides a *MemorySearcher as search.Backend.
func TestModule_ProvidesMemoryBackend(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var got Backend
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		Module,
		fx.Populate(&got),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	if _, ok := got.(*MemorySearcher); !ok {
		t.Errorf("expected *MemorySearcher from Module, got %T", got)
	}
}
