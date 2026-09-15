// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p reinterprets memory and proves the operation sound.
package p

// SAFETY: unsafe is imported only for the single proven conversion in Bytes.
import "unsafe"

// Bytes returns the string's backing array without copying it.
func Bytes(s string) []byte {
	// SAFETY: the returned slice aliases the string's immutable backing array,
	// stays within len(s), and is never written to by this package or its
	// callers, so no immutable memory is mutated and no bound is exceeded.
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
