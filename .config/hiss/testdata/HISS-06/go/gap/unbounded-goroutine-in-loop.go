// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p spawns one goroutine per input element.
package p

// FanOut starts len(backends) goroutines at once with no semaphore, no
// errgroup limit and no worker pool, so concurrency scales with untrusted
// input size rather than with a declared bound.
func FanOut(backends []func() int) []int {
	out := make([]int, len(backends))
	done := make(chan int, len(backends))
	for i, b := range backends {
		go func(i int, b func() int) {
			out[i] = b()
			done <- i
		}(i, b)
	}
	for range backends {
		<-done
	}
	return out
}
