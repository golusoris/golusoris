// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package search

import "testing"

func hitsOfLen(n int) []Hit {
	hits := make([]Hit, n)
	for i := range hits {
		hits[i] = Hit{Document: Document{"i": i}}
	}
	return hits
}

func TestPaginate_noOffsetNoLimit(t *testing.T) {
	t.Parallel()
	got := paginate(hitsOfLen(5), 0, 0)
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5 (no offset/limit is a no-op)", len(got))
	}
}

func TestPaginate_offsetWithinRange(t *testing.T) {
	t.Parallel()
	got := paginate(hitsOfLen(10), 3, 0)
	if len(got) != 7 {
		t.Fatalf("len = %d, want 7", len(got))
	}
	if got[0].Document["i"] != 3 {
		t.Fatalf("first hit = %v, want index 3", got[0].Document["i"])
	}
}

func TestPaginate_offsetAtEnd(t *testing.T) {
	t.Parallel()
	got := paginate(hitsOfLen(5), 5, 0)
	if got != nil {
		t.Fatalf("offset == len(hits) should empty the result, got %d hits", len(got))
	}
}

func TestPaginate_offsetPastEnd(t *testing.T) {
	t.Parallel()
	got := paginate(hitsOfLen(5), 100, 0)
	if got != nil {
		t.Fatalf("offset past len(hits) should empty the result, got %d hits", len(got))
	}
}

func TestPaginate_limitSmallerThanRemaining(t *testing.T) {
	t.Parallel()
	got := paginate(hitsOfLen(10), 0, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestPaginate_limitLargerThanRemaining(t *testing.T) {
	t.Parallel()
	got := paginate(hitsOfLen(3), 0, 100)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (limit larger than available is a no-op)", len(got))
	}
}

func TestPaginate_offsetAndLimitCombined(t *testing.T) {
	t.Parallel()
	got := paginate(hitsOfLen(10), 5, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Document["i"] != 5 {
		t.Fatalf("first hit = %v, want index 5", got[0].Document["i"])
	}
}
