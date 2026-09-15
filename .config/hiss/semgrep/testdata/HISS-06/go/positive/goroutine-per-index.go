// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "sync"

// FanOutIndexed counts to the length of the input, which is the same
// input-proportional fan-out written as a counted loop.
func FanOutIndexed(items []string, work func(string)) {
	var wg sync.WaitGroup
	wg.Add(len(items))
	for i := 0; i < len(items); i++ {
		go func() {
			defer wg.Done()
			work(items[i])
		}()
	}
	wg.Wait()
}
