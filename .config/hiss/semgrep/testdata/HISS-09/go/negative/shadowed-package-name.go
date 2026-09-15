// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// unsafeCounter is a local identifier whose name merely starts with the
// package name; nothing here reaches package unsafe.
type unsafeCounter struct{ n int }

// Bump advances the counter.
func (c *unsafeCounter) Bump() int {
	c.n++
	return c.n
}
