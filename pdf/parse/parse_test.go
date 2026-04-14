package parse_test

import (
	"context"
	"testing"

	"github.com/golusoris/golusoris/pdf/parse"
)

func TestParseTime_valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		zero bool
	}{
		{"D:20230415120000", false},
		{"D:20230415", false},
		{"", true},
		{"garbage", true},
	}
	for _, tc := range cases {
		got := parse.ParseTime(tc.in)
		if tc.zero && !got.IsZero() {
			t.Errorf("ParseTime(%q): expected zero, got %v", tc.in, got)
		}
		if !tc.zero && got.IsZero() {
			t.Errorf("ParseTime(%q): expected non-zero", tc.in)
		}
	}
}

func TestMerge_emptySlice(t *testing.T) {
	t.Parallel()
	// Merging an empty list should return nil, not panic.
	err := parse.Merge(context.Background(), nil, "out.pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
