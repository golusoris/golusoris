// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// FanOutGroup spawns through an errgroup that was never given a limit, so Go
// starts a goroutine per element immediately — the errgroup spelling of the
// same unbounded fan-out.
func FanOutGroup(ctx context.Context, items []string, work func(context.Context, string) error) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, it := range items {
		g.Go(func() error { return work(gctx, it) })
	}
	return g.Wait()
}
