// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// SAFETY: unsafe is imported only for the conversion in First.
import "unsafe"

// First reinterprets the slice header without a proof of the precondition.
func First(b []byte) *byte {
	return (*byte)(unsafe.Pointer(&b[0]))
}
