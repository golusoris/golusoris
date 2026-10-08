// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p allocates on every call of a hot path.
package p

// Sum allocates a fresh slice per call instead of reusing a caller-owned
// buffer. The allocation-budget gate protects named benchmarks; it cannot find
// an unbenchmarked hot path.
func Sum(xs []int) []int {
	out := make([]int, 0, len(xs))
	running := 0
	for _, x := range xs {
		running += x
		out = append(out, running)
	}
	return out
}
