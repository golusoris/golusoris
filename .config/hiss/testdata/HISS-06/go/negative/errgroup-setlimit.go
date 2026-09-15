// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// FanOutGroup bounds the fan-out with errgroup.SetLimit, which blocks Go until
// a slot frees.
func FanOutGroup(ctx context.Context, items []string, limit int, work func(context.Context, string) error) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	for _, it := range items {
		g.Go(func() error { return work(gctx, it) })
	}
	return g.Wait()
}
