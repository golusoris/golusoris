// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package redis provides a [rueidis] client as an fx module. rueidis
// auto-detects cluster vs standalone mode from InitAddress.
//
// Config key prefix: cache.redis.*
//
//	cache.redis.addr   = "localhost:6379"   # comma-sep for cluster
//	cache.redis.user   = ""                 # optional
//	cache.redis.pass   = ""                 # optional
//	cache.redis.db     = 0                  # standalone DB index
//	cache.redis.tls    = false
package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

// Options are the configuration knobs for the redis module.
type Options struct {
	// Addr is one address for standalone or multiple comma-separated
	// addresses for a standalone server or cluster (default "localhost:6379").
	Addr     string `koanf:"addr"`
	Username string `koanf:"user"`
	Password string `koanf:"pass"`
	// DB is the standalone database index (ignored in cluster mode).
	DB  int  `koanf:"db"`
	TLS bool `koanf:"tls"`
}

func defaultOptions() Options {
	return Options{Addr: "localhost:6379"}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("cache.redis", &opts); err != nil {
		return Options{}, fmt.Errorf("cache/redis: load options: %w", err)
	}
	return opts, nil
}

func newClient(opts Options, logger *slog.Logger) (rueidis.Client, error) {
	if logger == nil {
		return nil, errors.New("cache/redis: logger is required")
	}
	co := clientOption(opts)
	c, err := rueidis.NewClient(co)
	if err != nil {
		return nil, fmt.Errorf("cache/redis: new client: %w", err)
	}
	logger.Debug("cache/redis: connected", slog.String("addr", opts.Addr))
	return c, nil
}

// clientOption maps the validated module config onto rueidis transport settings.
func clientOption(opts Options) rueidis.ClientOption {
	addrs := strings.Split(opts.Addr, ",")
	for i, a := range addrs {
		addrs[i] = strings.TrimSpace(a)
	}
	co := rueidis.ClientOption{
		InitAddress: addrs,
		Username:    opts.Username,
		Password:    opts.Password,
		SelectDB:    opts.DB,
	}
	if opts.TLS {
		co.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return co
}

// Module provides rueidis.Client to the fx graph. Requires [Core] for
// config + log. Redis must already be running at start time (no retry
// logic — keep Redis always-available per Twelve-Factor §IV).
var Module = fx.Module(
	"golusoris.cache.redis",
	fx.Provide(loadOptions),
	fx.Provide(newClient),
	fx.Invoke(func(lc fx.Lifecycle, c rueidis.Client) {
		lc.Append(fx.Hook{
			OnStop: func(_ context.Context) error {
				c.Close()
				return nil
			},
		})
	}),
)
