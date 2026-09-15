// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "testing"

// TestClamp covers the three dimensions HISS-15 requires: a positive case in
// range, negative cases outside the range and with the range inverted, and the
// boundary cases at low, at high and at the integer extremes.
func TestClamp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		v, low, high int
		want         int
	}{
		{"positive/in-range", 5, 0, 10, 5},
		{"negative/below", -3, 0, 10, 0},
		{"negative/above", 42, 0, 10, 10},
		{"negative/inverted-range", 5, 10, 0, 10},
		{"boundary/at-low", 0, 0, 10, 0},
		{"boundary/at-high", 10, 0, 10, 10},
		{"boundary/min-int", -1 << 62, 0, 10, 0},
		{"boundary/max-int", 1<<62 - 1, 0, 10, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Clamp(tc.v, tc.low, tc.high); got != tc.want {
				t.Fatalf("Clamp(%d, %d, %d) = %d, want %d", tc.v, tc.low, tc.high, got, tc.want)
			}
		})
	}
}
