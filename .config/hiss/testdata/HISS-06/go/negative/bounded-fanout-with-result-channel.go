// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// BoundedCollect takes a semaphore slot before every spawn and also collects
// results over a channel from inside the goroutine. The bound is the send that
// precedes the 'go'; the result send must neither create nor remove one.
func BoundedCollect(items []string, limit int, work func(string) string) []string {
	sem := make(chan struct{}, limit)
	out := make(chan string, len(items))
	for _, it := range items {
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			out <- work(it)
		}()
	}
	res := make([]string, 0, len(items))
	for range items {
		res = append(res, <-out)
	}
	return res
}
