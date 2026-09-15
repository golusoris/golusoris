// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// TwoGroups bounds one group and fans out on the other. The limit belongs to
// 'inner'; the loop spawns into 'outer', which is unbounded. Only a SetLimit on
// the same group as the Go call may suppress the report.
func TwoGroups(ctx context.Context, items []string, work func(context.Context, string) error) error {
	inner, ictx := errgroup.WithContext(ctx)
	inner.SetLimit(4)
	inner.Go(func() error { return work(ictx, "warm-up") })

	outer, octx := errgroup.WithContext(ctx)
	for _, it := range items {
		outer.Go(func() error { return work(octx, it) })
	}
	if err := inner.Wait(); err != nil {
		return err
	}
	return outer.Wait()
}
