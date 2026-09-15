// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p demonstrates a loop with no exit condition.
package p

// Drain loops with no scalar upper bound and no exit condition.
func Drain(ch <-chan int) int {
	total := 0
	for {
		total += <-ch
	}
}
