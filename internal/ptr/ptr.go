// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ptr holds pointer helpers shared by in-memory stores.
package ptr

// Clone returns a pointer to a copy of *p, or nil when p is nil, so a store
// never hands out a pointer into its own state.
func Clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	cloned := *p
	return &cloned
}
