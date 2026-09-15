// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p aborts the process instead of returning an error.
package p

// MustPositive replaces error propagation with a process abort.
func MustPositive(n int) int {
	if n <= 0 {
		panic("n must be positive")
	}
	return n
}
