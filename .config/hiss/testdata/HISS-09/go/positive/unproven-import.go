// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "unsafe"

// Size reports the machine size of a value without stating why the
// reinterpretation is sound.
func Size(v uint64) uintptr {
	return unsafe.Sizeof(v)
}
