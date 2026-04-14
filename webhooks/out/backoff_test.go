package out

import (
	"testing"
	"time"
)

func TestExponentialBackoff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{9, 5 * time.Minute}, // 512s > 300s → cap at 5 min
	}
	for _, tc := range cases {
		got := exponentialBackoff(tc.attempt)
		if got != tc.want {
			t.Errorf("exponentialBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}
