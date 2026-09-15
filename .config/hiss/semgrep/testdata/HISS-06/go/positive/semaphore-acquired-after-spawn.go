// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// AcquireAfter takes the slot one statement too late: the goroutine for this
// element is already running, so the loop bounds the iteration rate rather
// than the live goroutine count.
func AcquireAfter(ctx context.Context, items []string, sem *semaphore.Weighted, work func(string)) error {
	for _, it := range items {
		go func() {
			defer sem.Release(1)
			work(it)
		}()
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
	}
	return nil
}
