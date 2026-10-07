// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package hook owns the fx lifecycle both leader backends share: the
// election loop runs from OnStart until OnStop cancels it.
package hook

import (
	"context"
	"fmt"
	"log/slog"

	"go.uber.org/fx"
)

// RunUntilStop runs run on its own goroutine from OnStart until OnStop
// cancels it. OnStop waits for run to return, bounded by the stop context fx
// supplies, so a wedged elector cannot hang shutdown past fx's stop timeout.
func RunUntilStop(lc fx.Lifecycle, logger *slog.Logger, name string, run func(context.Context) error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				if err := run(ctx); err != nil {
					logger.ErrorContext(ctx, name+": run failed", slog.String("error", err.Error()))
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
				return fmt.Errorf("%s: elector did not stop before the stop deadline: %w", name, stopCtx.Err())
			}
		},
	})
}
