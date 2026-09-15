// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p reinterprets memory with no proof that it is sound.
package p

import "unsafe"

// Bytes reinterprets a string's backing array as a byte slice. No proof
// comment precedes the statement, so nothing states why the aliasing is sound
// or why nothing mutates the result.
func Bytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
