// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package p is the after-state of a signature narrowing.
package p

import "context"

// Keep now requires a context it did not require before. Every existing caller
// stops compiling, which is a breaking change to a published contract, and the
// commit that makes it needs only a Conventional-Commits subject to land.
func Keep(ctx context.Context, s string) string {
	_ = ctx
	return s
}
