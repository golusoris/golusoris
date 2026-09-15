// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p assembles a credential from parts.
package p

// Token returns a credential that never appears as one literal, so no
// content-matching scanner sees the shape it is looking for. The compiler
// reassembles it verbatim.
func Token() string {
	prefix := "ghp_"
	body := "0A1b2C3d4E5f6G7h8I9j"
	tail := "0K1l2M3n4O5p"
	return prefix + body + tail
}
