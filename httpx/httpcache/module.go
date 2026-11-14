// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/twotier"
	"github.com/golusoris/golusoris/core/config"
)

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("http.outbound.cache", &opts); err != nil {
		return Options{}, fmt.Errorf("httpcache: load options: %w", err)
	}
	return opts, nil
}

type moduleParams struct {
	fx.In
	Options Options
	L1      *memory.Cache
	Logger  *slog.Logger
	Redis   rueidis.Client `optional:"true"`
}

func newModuleCache(p moduleParams) (*Cache, error) {
	if err := validateOptions(p.Options); err != nil {
		return nil, err
	}
	var dependencies []twotier.Option
	if p.Options.Store.L2 != twotier.L2None {
		dependencies = append(dependencies, twotier.WithRedis(p.Redis))
	}
	store, err := twotier.New(p.L1, storeOptions(p.Options), p.Logger, dependencies...)
	if err != nil {
		return nil, fmt.Errorf("httpcache: build store: %w", err)
	}
	return New(p.Options, store, p.Logger)
}

func registerClose(lc fx.Lifecycle, cache *Cache) {
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return cache.Close() }})
}

// Module provides a Cache, requiring config, memory, logger and Redis unless store.l2=none.
var Module = fx.Module("golusoris.httpx.httpcache", fx.Provide(loadOptions, newModuleCache), fx.Invoke(registerClose))

func storeOptions(opts Options) twotier.Options {
	ttl := opts.Store.L1TTL
	if opts.Store.L2 == twotier.L2None {
		ttl = opts.Store.Retention
	}
	return twotier.Options{L1TTL: ttl, L2TTL: opts.Store.Retention, L2: opts.Store.L2}
}
