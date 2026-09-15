// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "sync"

// Workers starts a fixed number of consumers: the goroutine count is a
// constant of the program, not a function of the input.
func Workers(jobs <-chan string, work func(string)) {
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				work(j)
			}
		}()
	}
	wg.Wait()
}
