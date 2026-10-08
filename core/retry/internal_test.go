// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package retry

import (
	"math"
	"testing"
	"time"
)

func TestGrowSaturates(t *testing.T) {
	t.Parallel()
	if got := grow(time.Second, 2, 3*time.Second); got != 2*time.Second {
		t.Fatalf("grow = %v, want 2s", got)
	}
	if got := grow(2*time.Second, 2, 3*time.Second); got != 3*time.Second {
		t.Fatalf("grow = %v, want cap 3s", got)
	}
	if got := grow(time.Duration(math.MaxInt64/2+1), 4, time.Duration(math.MaxInt64)); got != time.Duration(math.MaxInt64) {
		t.Fatalf("grow overflow = %v, want saturation", got)
	}
}

func TestJitteredBounds(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		jitter, r float64
		want      time.Duration
	}{
		{0, 0.99, time.Second},
		{0.2, 0, time.Second},
		{0.2, 0.5, 900 * time.Millisecond},
		{1, 0.5, 500 * time.Millisecond},
	} {
		if got := jittered(time.Second, tt.jitter, tt.r); got != tt.want {
			t.Errorf("jittered(1s, %v, %v) = %v, want %v", tt.jitter, tt.r, got, tt.want)
		}
	}
}

func TestUnitRandomRange(t *testing.T) {
	t.Parallel()
	for range 1000 {
		if r := unitRandom(); r < 0 || r >= 1 {
			t.Fatalf("unitRandom = %v, want [0, 1)", r)
		}
	}
}
