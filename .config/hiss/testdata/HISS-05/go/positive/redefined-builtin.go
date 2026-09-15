// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p rebinds a predeclared identifier.
package p

// Count rebinds the predeclared identifier len, so every later use inside the
// function means something other than the builtin.
func Count(xs []int) int {
	len := 0
	for range xs {
		len++
	}
	return len
}
