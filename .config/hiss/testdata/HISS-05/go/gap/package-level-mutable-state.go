// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p keeps mutable state at package scope.
package p

// counter is mutable state at the widest scope in the package, reachable and
// writable from every function in it. HISS-05 wants the narrowest scope that
// works; no linter enabled in .golangci.yml decides this, because
// gochecknoglobals is not in the enable list.
var counter int

// Bump mutates the package-scoped binding instead of an owned value.
func Bump() int {
	counter++
	return counter
}
