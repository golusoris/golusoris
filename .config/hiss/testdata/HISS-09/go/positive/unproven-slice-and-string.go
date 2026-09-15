// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// SAFETY: unsafe is imported only for the conversions below.
import "unsafe"

// Bytes builds a slice over caller memory with no stated proof that the
// length and the lifetime hold.
func Bytes(p *byte, n int) []byte {
	return unsafe.Slice(p, n)
}

// Text builds a string over the same memory, equally unproven.
func Text(p *byte, n int) string {
	return unsafe.String(p, n)
}
