// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p exports behaviour with no test of any dimension.
package p

// Clamp has no sibling test file: no positive case, no negative case and no
// boundary case, so its statements never execute under go test.
func Clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
