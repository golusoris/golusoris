// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nri

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	nristub "github.com/containerd/nri/pkg/stub"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

// Options tunes plugin registration and hook execution. Config keys live
// under the "nri" koanf prefix.
type Options struct {
	// Name is the plugin name used in NRI registration. Must be set together
	// with Index — set alone it is silently ignored by containerd/nri, which
	// then re-derives both from the binary's filename instead (see
	// newRegistration). Leave both empty to defer to the NRI_PLUGIN_NAME /
	// NRI_PLUGIN_IDX env vars the runtime sets when it launches the plugin.
	Name string `koanf:"name"`
	// Index controls ordering among plugins registered with the same
	// runtime (two lexical digits, "00"-"99"). Must be set together with
	// Name; see Name's doc.
	Index string `koanf:"index"`
	// HookTimeout bounds every lifecycle hook call.
	HookTimeout time.Duration `koanf:"hook_timeout"`
}

func defaultOptions() Options {
	return Options{HookTimeout: nristub.DefaultRequestTimeout}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("nri", &opts); err != nil {
		return Options{}, fmt.Errorf("nri: load options: %w", err)
	}
	return opts, nil
}

// ProvideHooks wires the app's [Hooks] into the fx graph for [Module] to
// consume. Exactly one call per app — Module fails to build without it.
func ProvideHooks(hooks Hooks) fx.Option {
	return fx.Supply(hooks)
}

// newPlugin is the fx constructor for [Registration].
func newPlugin(opts Options, hooks Hooks) (*Registration, error) {
	return New(opts, hooks)
}

// runPlugin starts the plugin on fx Start in its own goroutine (Run blocks
// until the context is cancelled or the connection drops) and stops it on fx
// Stop, waiting for that goroutine to actually exit before OnStop returns —
// bounded by the OnStop context fx supplies, so a wedged Run cannot hang
// shutdown past fx's own stop timeout (HISS-02: context timeout on all I/O).
// A plugin exit before shutdown triggers an app-wide shutdown, mirroring
// k8s/operator's runManager.
func runPlugin(lc fx.Lifecycle, reg *Registration, logger *slog.Logger, sd fx.Shutdowner) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				if err := reg.Stub.Run(ctx); err != nil && ctx.Err() == nil {
					logger.Error("nri: plugin exited", slog.Any("err", err))
					if serr := sd.Shutdown(); serr != nil {
						logger.Error("nri: shutdown request failed", slog.Any("err", serr))
					}
				}
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
				return nil
			case <-stopCtx.Done():
				return fmt.Errorf("nri: plugin did not stop before the OnStop context expired: %w", stopCtx.Err())
			}
		},
	})
}

// Module registers an NRI plugin and runs it under the fx lifecycle. Apps
// supply their lifecycle callbacks via [ProvideHooks]. Requires
// [golusoris.Core] for config + log.
var Module = fx.Module(
	"golusoris.k8s.nri",
	fx.Provide(loadOptions),
	fx.Provide(newPlugin),
	fx.Invoke(runPlugin),
)
