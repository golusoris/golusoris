// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dra

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/k8s/podinfo"
)

// Source discovers the node's devices; [Module] calls it once on start,
// bounded by fx's start context. Later changes go through
// [Publisher.Update].
type Source func(ctx context.Context) ([]Device, error)

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("k8s.dra", &opts); err != nil {
		return Options{}, fmt.Errorf("dra: load options: %w", err)
	}
	return opts, nil
}

type params struct {
	fx.In

	Lifecycle fx.Lifecycle
	Options   Options
	Logger    *slog.Logger
	Client    kubernetes.Interface `optional:"true"`
	Pod       podinfo.PodInfo      `optional:"true"`
}

// Module provides a *Publisher that publishes src's devices on start and
// deletes the node's slices on stop. Requires kubernetes.Interface (from
// k8s/client) when k8s.dra.enabled; k8s.dra.node defaults to the
// podinfo.PodInfo node name. Disabled, the Publisher's Update only
// validates.
func Module(src Source) fx.Option {
	return fx.Module(
		"golusoris.k8s.dra",
		fx.Provide(loadOptions, func(p params) (*Publisher, error) { return provide(p, src) }),
		fx.Invoke(func(*Publisher) {}),
	)
}

func provide(p params, src Source) (*Publisher, error) {
	if !p.Options.Enabled {
		return &Publisher{disabled: true, logger: p.Logger}, nil
	}
	opts := p.Options
	if opts.Node == "" {
		opts.Node = p.Pod.NodeName
	}
	pub, err := NewPublisher(p.Client, opts, p.Logger)
	if err != nil {
		return nil, err
	}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var devices []Device
			if src != nil {
				var srcErr error
				if devices, srcErr = src(ctx); srcErr != nil {
					return fmt.Errorf("dra: source: %w", srcErr)
				}
			}
			return pub.Start(ctx, devices)
		},
		OnStop: pub.Stop,
	})
	return pub, nil
}
