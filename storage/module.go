package storage

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options selects and tunes the storage backend.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    storage.Module, // provides storage.Bucket
//	)
//
// Config keys live under the "storage" prefix.
type Options struct {
	// Backend selects the storage backend: "local" (default) or "s3"
	// (S3/MinIO-compatible). GCS/Azure are future backends.
	Backend string `koanf:"backend"`
	// Local configures the local-filesystem backend.
	Local LocalOptions `koanf:"local"`
	// S3 configures the S3-compatible backend (used when backend = "s3").
	S3 S3Options `koanf:"s3"`
}

// LocalOptions configures the local-filesystem backend.
type LocalOptions struct {
	// Path is the base directory for stored objects (default "./data").
	Path string `koanf:"path"`
}

func defaultOptions() Options {
	return Options{
		Backend: "local",
		Local:   LocalOptions{Path: "./data"},
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("storage", &opts); err != nil {
		return Options{}, fmt.Errorf("storage: load options: %w", err)
	}
	return opts, nil
}

func newBucket(opts Options, logger *slog.Logger) (Bucket, error) {
	switch opts.Backend {
	case "local", "":
		b, err := NewLocalBucket(opts.Local.Path)
		if err != nil {
			return nil, fmt.Errorf("storage: build local backend: %w", err)
		}
		logger.Debug("storage: started",
			slog.String("backend", "local"),
			slog.String("path", opts.Local.Path),
		)
		return b, nil
	case "s3":
		// Bounded init: a background context is fine here — the AWS config
		// load is the only blocking call and fx applies its own start timeout.
		b, err := NewS3Bucket(context.Background(), opts.S3)
		if err != nil {
			return nil, fmt.Errorf("storage: build s3 backend: %w", err)
		}
		logger.Debug("storage: started",
			slog.String("backend", "s3"),
			slog.String("bucket", opts.S3.Bucket),
			slog.String("endpoint", opts.S3.Endpoint),
			slog.Bool("path_style", opts.S3.PathStyle),
		)
		return b, nil
	default:
		return nil, fmt.Errorf("storage: unknown backend %q", opts.Backend)
	}
}

// Module provides storage.Bucket to the fx graph.
var Module = fx.Module("golusoris.storage",
	fx.Provide(loadOptions),
	fx.Provide(newBucket),
)
