// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p demonstrates a non-DAG control-flow jump.
package p

// Sum leaves the loop with a backward jump instead of structured control flow.
func Sum(xs []int) int {
	total := 0
	i := 0
loop:
	if i < len(xs) {
		total += xs[i]
		i++
		goto loop
	}
	return total
}
