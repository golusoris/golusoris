// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p exports branchless behaviour with a happy-path test only.
package p

// Double is branchless, so one assertion executes every statement in it.
func Double(n int) int {
	return n * 2
}
