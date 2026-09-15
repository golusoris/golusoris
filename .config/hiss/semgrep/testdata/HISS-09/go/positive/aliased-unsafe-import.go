// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p reaches package unsafe under another name.
package p

import raw "unsafe"

// Bytes performs exactly the operation the positive fixture performs, with no
// proof, but reaches the package through an import alias. The scanner matches
// the selector's package identifier spelled "unsafe" and nothing else.
func Bytes(s string) []byte {
	return raw.Slice(raw.StringData(s), len(s))
}
