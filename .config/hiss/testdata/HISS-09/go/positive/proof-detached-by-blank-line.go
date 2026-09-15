// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// SAFETY: unsafe is imported only for the conversion in Second.
import "unsafe"

// Second states a proof but lets a blank line detach it from the conversion,
// so the proof no longer covers the statement it is meant to justify.
func Second(b []byte) *byte {
	// SAFETY: every caller checks len(b) > 0.

	return (*byte)(unsafe.Pointer(&b[0]))
}
