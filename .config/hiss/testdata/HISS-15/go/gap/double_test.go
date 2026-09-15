// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "testing"

// TestDouble asserts the positive dimension only. The negative dimension
// (a negative input) and the boundary dimension (zero, and the overflow point
// at the integer extremes) are both absent, yet statement coverage of Double is
// 100%: an aggregate coverage floor cannot see a missing dimension.
func TestDouble(t *testing.T) {
	t.Parallel()
	if got := Double(21); got != 42 {
		t.Fatalf("Double(21) = %d, want 42", got)
	}
}
