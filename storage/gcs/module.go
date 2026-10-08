// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/storage"
)

// initTimeout bounds credential detection and client construction; fx runs
// constructors without a deadline of its own.
const initTimeout = 15 * time.Second

type params struct {
	fx.In
	Config *config.Config
	Logger *slog.Logger
	Clock  clock.Clock
	LC     fx.Lifecycle
}

// Module provides [storage.Bucket] backed by GCS. Use it instead of
// storage.Module; config keys live under "storage.gcs".
var Module = fx.Module(
	"golusoris.storage.gcs",
	fx.Provide(newFromConfig),
)

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("storage.gcs", &opts); err != nil {
		return Options{}, fmt.Errorf("storage/gcs: load options: %w", err)
	}
	return opts, nil
}

func newFromConfig(p params) (storage.Bucket, error) {
	opts, err := loadOptions(p.Config)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	b, err := New(ctx, opts, p.Clock)
	if err != nil {
		return nil, err
	}
	p.LC.Append(fx.Hook{OnStop: func(context.Context) error { return b.Close() }})
	p.Logger.Debug("storage: started",
		slog.String("backend", "gcs"),
		slog.String("bucket", opts.Bucket),
		slog.String("endpoint", opts.Endpoint),
	)
	return b, nil
}
