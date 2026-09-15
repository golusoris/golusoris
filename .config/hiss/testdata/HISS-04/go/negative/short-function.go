// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds a function inside every HISS-04 cap.
package p

// Accumulate is short, branchless and well inside the caps.
func Accumulate(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}
