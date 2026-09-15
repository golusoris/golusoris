// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p bounds its fan-out by construction.
package p

// maxWorkers is the declared scalar bound on concurrency.
const maxWorkers = 8

// FanOut starts exactly maxWorkers goroutines regardless of input size, so
// concurrency is bounded by a constant rather than by the caller.
func FanOut(jobs <-chan func() int, results chan<- int) {
	for w := 0; w < maxWorkers; w++ {
		go func() {
			for job := range jobs {
				results <- job()
			}
		}()
	}
}
