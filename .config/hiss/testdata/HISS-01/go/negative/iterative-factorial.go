// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds the acyclic equivalent of the positive fixtures.
package p

// Factorial is iterative, so the call graph stays a DAG.
func Factorial(n int) int {
	total := 1
	for i := 2; i <= n; i++ {
		total *= i
	}
	return total
}
