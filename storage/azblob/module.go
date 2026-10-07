// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob

import (
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/storage"
)

type params struct {
	fx.In
	Config *config.Config
	Logger *slog.Logger
	Clock  clock.Clock
}

// Module provides [storage.Bucket] backed by Azure Blob Storage. Use it
// instead of storage.Module; config keys live under "storage.azblob".
var Module = fx.Module(
	"golusoris.storage.azblob",
	fx.Provide(newFromConfig),
)

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("storage.azblob", &opts); err != nil {
		return Options{}, fmt.Errorf("storage/azblob: load options: %w", err)
	}
	return opts, nil
}

func newFromConfig(p params) (storage.Bucket, error) {
	opts, err := loadOptions(p.Config)
	if err != nil {
		return nil, err
	}
	b, err := New(opts, p.Clock)
	if err != nil {
		return nil, err
	}
	auth := "entra-id"
	if opts.AccountKey != "" {
		auth = "shared-key"
	}
	p.Logger.Debug("storage: started",
		slog.String("backend", "azblob"),
		slog.String("container", opts.Container),
		slog.String("service_url", opts.ServiceURL),
		slog.String("auth", auth),
	)
	return b, nil
}
