// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// SAFETY: unsafe is imported only for the single proven conversion in First.
import "unsafe"

// First reinterprets the slice header under a stated proof.
func First(b []byte) *byte {
	// SAFETY: every caller checks len(b) > 0, so &b[0] is inside the allocation.
	return (*byte)(unsafe.Pointer(&b[0]))
}
