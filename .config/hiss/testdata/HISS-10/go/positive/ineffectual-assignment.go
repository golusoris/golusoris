// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p assigns a value that is never read.
package p

// Total overwrites sum before anything reads the first assignment.
func Total(xs []int) int {
	sum := 1
	sum = 0
	for _, x := range xs {
		sum += x
	}
	return sum
}
