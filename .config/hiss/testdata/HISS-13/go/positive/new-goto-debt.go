// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p adds one new infraction to the tree.
package p

// Sum introduces a HISS-01 violation, raising total_infractions above the
// zero recorded in .standards-baseline.json.
func Sum(xs []int) int {
	total := 0
	i := 0
loop:
	if i < len(xs) {
		total += xs[i]
		i++
		goto loop
	}
	return total
}
