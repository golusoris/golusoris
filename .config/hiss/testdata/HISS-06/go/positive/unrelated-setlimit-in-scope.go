// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "golang.org/x/time/rate"

// Throttled calls SetLimit on a rate.Limiter, which has nothing to do with
// goroutine bounding, and then spawns one goroutine per element. A SetLimit on
// any receiver other than the group being spawned into must not disarm the
// rule — least of all for a bare 'go' statement, which no SetLimit can bound.
func Throttled(items []string, lim *rate.Limiter, work func(string)) {
	lim.SetLimit(10)
	for _, it := range items {
		go work(it)
	}
}
