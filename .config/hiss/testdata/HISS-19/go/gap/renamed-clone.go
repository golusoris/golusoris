// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p holds the same behaviour under different identifiers.
package p

import "strings"

// TidyCaption performs exactly the normalisation the positive pair performs,
// with every local renamed and the two guards reordered. The scan compares
// normalised token sequences, so a rename plus a reorder is enough to miss it.
func TidyCaption(raw string) string {
	stripped := strings.TrimSpace(raw)
	folded := strings.ToLower(stripped)
	squeezed := strings.Join(strings.Fields(folded), " ")
	if len(squeezed) > 64 {
		return squeezed[:64]
	}
	if squeezed == "" {
		return "unknown"
	}
	return squeezed
}
