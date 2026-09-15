// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// FanOutWeighted acquires a weighted semaphore before every spawn, so the
// pool size caps the live goroutines.
func FanOutWeighted(ctx context.Context, items []string, sem *semaphore.Weighted, work func(string)) error {
	for _, it := range items {
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		go func() {
			defer sem.Release(1)
			work(it)
		}()
	}
	return nil
}
