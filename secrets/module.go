package secrets

import (
	"errors"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options selects and configures the Secret backend.
//
// Config key prefix: secrets.* (e.g. secrets.backend, secrets.file.dir).
type Options struct {
	// Backend selects the implementation: "env" (default) or "file".
	Backend string `koanf:"backend"`
	// File configures the file backend (used when Backend == "file").
	File FileOptions `koanf:"file"`
}

// FileOptions configures the file-based backend.
type FileOptions struct {
	// Dir is the directory whose file names are keys and contents are values.
	Dir string `koanf:"dir"`
}

func defaultOptions() Options {
	return Options{Backend: "env"}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("secrets", &opts); err != nil {
		return Options{}, fmt.Errorf("secrets: load options: %w", err)
	}
	return opts, nil
}

func newSecret(opts Options, logger *slog.Logger) (Secret, error) {
	switch opts.Backend {
	case "", "env":
		logger.Debug("secrets: started", slog.String("backend", "env"))
		return Env(), nil
	case "file":
		if opts.File.Dir == "" {
			return nil, errors.New("secrets: file backend requires secrets.file.dir")
		}
		logger.Debug("secrets: started",
			slog.String("backend", "file"),
			slog.String("dir", opts.File.Dir),
		)
		return File(opts.File.Dir), nil
	default:
		return nil, fmt.Errorf("secrets: unknown backend %q", opts.Backend)
	}
}

// Module provides secrets.Secret to the fx graph.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    secrets.Module, // provides secrets.Secret
//	)
//
// Config key prefix: secrets.* (e.g. secrets.backend, secrets.file.dir).
var Module = fx.Module("golusoris.secrets",
	fx.Provide(loadOptions),
	fx.Provide(newSecret),
)
