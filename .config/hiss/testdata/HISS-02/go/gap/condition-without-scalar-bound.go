// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds an unbounded loop that carries a condition.
package p

// Drain has no scalar upper bound -- the loop runs until a peer decides to
// stop sending -- but it carries a condition, and the scanner only reports a
// loop whose condition is absent.
func Drain(next func() (int, bool)) int {
	total := 0
	done := false
	for !done {
		v, ok := next()
		total += v
		done = !ok
	}
	return total
}
