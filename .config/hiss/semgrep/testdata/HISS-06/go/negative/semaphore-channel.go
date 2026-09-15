// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "sync"

// FanOut runs at most cap(sem) element queries at once: the send blocks the
// spawning loop once the pool is full, so the goroutine count is bounded by
// the semaphore, not by len(items).
func FanOut(items []string, limit int, work func(string)) {
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	wg.Add(len(items))
	for _, it := range items {
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			work(it)
		}()
	}
	wg.Wait()
}
