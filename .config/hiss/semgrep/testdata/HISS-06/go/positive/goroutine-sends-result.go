// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// Collect fans out one goroutine per element and collects the results over a
// channel. The only send in the loop happens INSIDE the spawned goroutine, so
// it bounds nothing: every element is already running by the time anything is
// sent. This is the commonest unbounded fan-out idiom in Go, and it must be
// reported exactly like the send-free shape next to it.
func Collect(items []string, work func(string) string) []string {
	out := make(chan string, len(items))
	for _, it := range items {
		go func() {
			out <- work(it)
		}()
	}
	res := make([]string, 0, len(items))
	for range items {
		res = append(res, <-out)
	}
	return res
}
