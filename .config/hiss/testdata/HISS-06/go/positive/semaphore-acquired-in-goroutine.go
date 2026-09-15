// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// AcquireInside acquires the semaphore inside the goroutine instead of before
// spawning it. The pool bounds how many goroutines run the work, not how many
// exist: len(items) goroutines are alive immediately, each blocked on Acquire.
func AcquireInside(ctx context.Context, items []string, sem *semaphore.Weighted, work func(string)) {
	for _, it := range items {
		go func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			work(it)
		}()
	}
}
