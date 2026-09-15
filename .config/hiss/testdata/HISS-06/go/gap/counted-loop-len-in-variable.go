// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// CountedVar is an unbounded fan-out the rule does not report: the loop bound
// is a variable that happens to hold len(items), and the pattern only matches
// 'i < len(x)' literally. Deciding that n is len(items) needs dataflow the
// rule does not have.
func CountedVar(items []string, work func(int)) {
	n := len(items)
	for i := 0; i < n; i++ {
		go work(i)
	}
}
