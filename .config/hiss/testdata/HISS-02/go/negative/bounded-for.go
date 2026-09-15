// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds the bounded equivalent of the positive fixture.
package p

// maxDrain is the scalar upper bound HISS-02 requires.
const maxDrain = 1024

// Drain reads at most maxDrain values, so the loop terminates.
func Drain(ch <-chan int) int {
	total := 0
	for i := 0; i < maxDrain; i++ {
		total += <-ch
	}
	return total
}
