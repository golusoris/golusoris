// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
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
	// (S3/MinIO-compatible). GCS lives in the storage/gcs module, whose
	// Module replaces this one.
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

const s3InitTimeout = 15 * time.Second

type s3BucketFactory func(context.Context, S3Options) (*S3Bucket, error)

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
	return newBucketWithS3Factory(opts, logger, s3InitTimeout, NewS3Bucket)
}

func newBucketWithS3Factory(
	opts Options, logger *slog.Logger, timeout time.Duration, factory s3BucketFactory,
) (Bucket, error) {
	switch opts.Backend {
	case "local", "":
		b, err := NewLocalBucket(opts.Local.Path)
		if err != nil {
			return nil, fmt.Errorf("storage: build local backend: %w", err)
		}
		logger.Debug(
			"storage: started",
			slog.String("backend", "local"),
			slog.String("path", opts.Local.Path),
		)
		return b, nil
	case "s3":
		if timeout <= 0 {
			return nil, fmt.Errorf("storage: s3 init timeout must be positive: %s", timeout)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		b, err := factory(ctx, opts.S3)
		if err != nil {
			return nil, fmt.Errorf("storage: build s3 backend: %w", err)
		}
		logger.Debug(
			"storage: started",
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
var Module = fx.Module(
	"golusoris.storage",
	fx.Provide(loadOptions),
	fx.Provide(newBucket),
)
