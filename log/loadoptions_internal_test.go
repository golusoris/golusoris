package log

import (
	"log/slog"
	"testing"

	"github.com/golusoris/golusoris/config"
)

// TestLoadOptionsHonorsEnvPrefix verifies the level/format come from the
// prefixed env vars via the config tree (#234).
func TestLoadOptionsHonorsEnvPrefix(t *testing.T) {
	t.Setenv("APP_LOG_LEVEL", "debug")
	t.Setenv("APP_LOG_FORMAT", "json")

	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts := loadOptions(cfg)
	if opts.Level != slog.LevelDebug {
		t.Errorf("Level = %v, want debug (APP_LOG_LEVEL must be honored)", opts.Level)
	}
	if opts.Format != FormatJSON {
		t.Errorf("Format = %q, want json", opts.Format)
	}
}

// TestLoadOptionsIgnoresUnprefixedLevel is the #234 regression: with a prefix
// configured, a bare LOG_LEVEL must NOT leak through (the old code read it via
// os.Getenv and silently overrode the prefixed config).
func TestLoadOptionsIgnoresUnprefixedLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug") // bare, no APP_ prefix

	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	if opts := loadOptions(cfg); opts.Level != slog.LevelInfo {
		t.Errorf("Level = %v, want info (a bare LOG_LEVEL must be ignored under a prefix)", opts.Level)
	}
}
