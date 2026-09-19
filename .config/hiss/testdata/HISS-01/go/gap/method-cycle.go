// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds a cycle that runs through methods.
package p

// Walker carries the cycle on its method set rather than on package-level
// functions.
type Walker struct{}

// Step reaches Hop, which reaches Step. The package call graph decides cycles
// between plain functions, but a call through a receiver is not resolved to a
// graph node, so this cycle goes unreported.
func (w Walker) Step(n int) bool {
	if n == 0 {
		return true
	}
	return w.Hop(n - 1)
}

// Hop closes the cycle.
func (w Walker) Hop(n int) bool {
	if n == 0 {
		return false
	}
	return w.Step(n - 1)
}
