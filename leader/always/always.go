// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package always is the leader backend for standalone single-replica
// runs: the replica is leader from start to stop without any lock, so
// singleton tasks written against [leader.Callbacks] run unchanged where
// no Lease or PostgreSQL is available. Each callback fires once per run:
// OnNewLeader, OnStartedLeading, then OnStoppedLeading on stop.
//
// Never run it with more than one replica: every replica would lead.
//
// Config keys (env: APP_LEADER_*), shared with the other backends:
//
//	leader.enabled  # master switch (default false)
//	leader.identity # identity passed to OnNewLeader (default hostname)
package always

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/leader"
	"github.com/golusoris/golusoris/leader/internal/hook"
)

// Options tunes the backend.
type Options struct {
	Enabled  bool   `koanf:"enabled"`
	Identity string `koanf:"identity"`
}

// DefaultOptions returns disabled options with the hostname identity.
func DefaultOptions() Options { return Options{} }

// Run leads until ctx is canceled, firing each callback once.
func Run(ctx context.Context, opts Options, cb leader.Callbacks) error {
	hook.Lead(ctx, hook.Identity(opts.Identity), cb)
	return nil
}

func loadOptionsAt(cfg *config.Config, path string) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal(path, &opts); err != nil {
		return Options{}, fmt.Errorf("leader/always: load options: %w", err)
	}
	return opts, nil
}

func loadOptions(cfg *config.Config) (Options, error) { return loadOptionsAt(cfg, "leader") }

// Module leads from fx start to stop. `leader.enabled=false` skips wiring.
func Module(cb leader.Callbacks) fx.Option {
	return fx.Module(
		"golusoris.leader.always",
		fx.Provide(loadOptions),
		fx.Invoke(func(lc fx.Lifecycle, opts Options, logger *slog.Logger) {
			wire(lc, opts, logger, "leader/always", cb)
		}),
	)
}

// NamedModule adds one more always-leader election keyed by key, with
// options under leader.elections.<key> and a *leader.Status tagged
// `name:"<key>"`, mirroring the NamedModule of the other backends.
func NamedModule(key string, cb leader.Callbacks) fx.Option {
	status, tag, err := hook.NamedStatus(key)
	if err != nil {
		return fx.Error(fmt.Errorf("leader/always: %w", err))
	}
	return fx.Module(
		"golusoris.leader.always."+key,
		status,
		fx.Invoke(fx.Annotate(
			func(lc fx.Lifecycle, cfg *config.Config, logger *slog.Logger, st *leader.Status) error {
				opts, loadErr := loadOptionsAt(cfg, hook.ConfigPath(key))
				if loadErr != nil {
					return loadErr
				}
				wire(lc, opts, logger, "leader/always["+key+"]", st.Observe(cb))
				return nil
			},
			fx.ParamTags("", "", "", tag),
		)),
	)
}

func wire(lc fx.Lifecycle, opts Options, logger *slog.Logger, name string, cb leader.Callbacks) {
	if !opts.Enabled {
		return
	}
	hook.RunUntilStop(lc, logger, name, func(ctx context.Context) error {
		return Run(ctx, opts, cb)
	})
}
