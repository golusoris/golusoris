// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package testctx provides test contexts with deadlines and automatic cleanup.
package testctx

import (
	"context"
	"testing"
	"time"
)

// Deadline derives a context from tb with a positive timeout d and cancels it during cleanup.
func Deadline(tb testing.TB, d time.Duration) context.Context {
	tb.Helper()
	if d <= 0 {
		tb.Fatalf("testctx: deadline duration must be positive, got %v", d)
	}
	ctx, cancel := context.WithTimeout(tb.Context(), d)
	tb.Cleanup(cancel)
	return ctx
}
