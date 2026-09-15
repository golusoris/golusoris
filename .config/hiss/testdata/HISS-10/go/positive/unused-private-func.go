// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p keeps an unexported function nothing calls.
package p

// Total is the package's only reachable entry point.
func Total(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}

// helper is unexported and unreferenced anywhere in the package.
func helper(x int) int {
	return x * 2
}
