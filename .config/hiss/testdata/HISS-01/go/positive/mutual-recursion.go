// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds a cycle that spans two functions.
package p

// IsEven reaches IsOdd, which reaches IsEven: a cycle no single-function
// scanner can see without a whole-program call graph.
func IsEven(n int) bool {
	if n == 0 {
		return true
	}
	return IsOdd(n - 1)
}

// IsOdd closes the cycle.
func IsOdd(n int) bool {
	if n == 0 {
		return false
	}
	return IsEven(n - 1)
}
