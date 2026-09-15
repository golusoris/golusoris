// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "sync"

// FanOut spawns one goroutine per element, so the live goroutine count
// follows the caller's input with no ceiling.
func FanOut(items []string, work func(string)) {
	var wg sync.WaitGroup
	wg.Add(len(items))
	for _, it := range items {
		go func() {
			defer wg.Done()
			work(it)
		}()
	}
	wg.Wait()
}
