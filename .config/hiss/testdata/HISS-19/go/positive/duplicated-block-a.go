// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds the first copy of a duplicated block.
package p

import "strings"

// NormalizeName is byte-for-byte identical to NormalizeLabel in the sibling
// file: one behaviour with two implementations.
func NormalizeName(in string) string {
	trimmed := strings.TrimSpace(in)
	lowered := strings.ToLower(trimmed)
	collapsed := strings.Join(strings.Fields(lowered), " ")
	if collapsed == "" {
		return "unknown"
	}
	if len(collapsed) > 64 {
		return collapsed[:64]
	}
	return collapsed
}
