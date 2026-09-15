// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package typesense

import (
	"testing"

	"github.com/golusoris/golusoris/search"
)

func TestBuildSearchParams_emptyQDefaultsToWildcard(t *testing.T) {
	t.Parallel()
	v := buildSearchParams(search.Query{})
	if got := v.Get("q"); got != "*" {
		t.Fatalf("q = %q, want %q", got, "*")
	}
	if got := v.Get("query_by"); got != "" {
		t.Fatalf("query_by = %q, want empty (no Fields given)", got)
	}
}

func TestBuildSearchParams_explicitQAndFields(t *testing.T) {
	t.Parallel()
	v := buildSearchParams(search.Query{Q: "shoe", Fields: []string{"name", "brand"}})
	if got := v.Get("q"); got != "shoe" {
		t.Fatalf("q = %q, want %q", got, "shoe")
	}
	if got := v.Get("query_by"); got != "name,brand" {
		t.Fatalf("query_by = %q, want %q", got, "name,brand")
	}
}

func TestBuildSearchParams_rawFilterTakesPrecedence(t *testing.T) {
	t.Parallel()
	v := buildSearchParams(search.Query{
		RawFilter: "price:>10",
		Filters:   map[string]any{"brand": "nike"},
	})
	if got := v.Get("filter_by"); got != "price:>10" {
		t.Fatalf("filter_by = %q, want RawFilter to win over Filters", got)
	}
}

func TestBuildSearchParams_filtersUsedWhenNoRawFilter(t *testing.T) {
	t.Parallel()
	v := buildSearchParams(search.Query{Filters: map[string]any{"brand": "nike"}})
	if got := v.Get("filter_by"); got != "brand:=nike" {
		t.Fatalf("filter_by = %q, want %q", got, "brand:=nike")
	}
}

func TestBuildSearchParams_noFilterWhenNeitherGiven(t *testing.T) {
	t.Parallel()
	v := buildSearchParams(search.Query{})
	if v.Has("filter_by") {
		t.Fatalf("filter_by should be unset, got %q", v.Get("filter_by"))
	}
}

func TestBuildSearchParams_sortAndLimit(t *testing.T) {
	t.Parallel()
	v := buildSearchParams(search.Query{SortBy: "price:desc", Limit: 20})
	if got := v.Get("sort_by"); got != "price:desc" {
		t.Fatalf("sort_by = %q, want %q", got, "price:desc")
	}
	if got := v.Get("per_page"); got != "20" {
		t.Fatalf("per_page = %q, want %q", got, "20")
	}
}

func TestBuildSearchParams_offsetWithLimitComputesPage(t *testing.T) {
	t.Parallel()
	// offset 40, limit 20 -> page 3 (1-indexed).
	v := buildSearchParams(search.Query{Offset: 40, Limit: 20})
	if got := v.Get("page"); got != "3" {
		t.Fatalf("page = %q, want %q", got, "3")
	}
}

func TestBuildSearchParams_offsetWithoutLimitFallsBackTo10(t *testing.T) {
	t.Parallel()
	// offset 25, no limit -> page size falls back to 10 -> page 3.
	v := buildSearchParams(search.Query{Offset: 25})
	if got := v.Get("page"); got != "3" {
		t.Fatalf("page = %q, want %q", got, "3")
	}
}

func TestBuildSearchParams_zeroOffsetOmitsPage(t *testing.T) {
	t.Parallel()
	v := buildSearchParams(search.Query{})
	if v.Has("page") {
		t.Fatalf("page should be unset when Offset is 0, got %q", v.Get("page"))
	}
}

func TestHitsFromTypesense_mapsDocumentAndScore(t *testing.T) {
	t.Parallel()
	in := []typesenseHit{
		{Document: search.Document{"id": "1"}, TextMatch: 42},
	}
	got := hitsFromTypesense(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Document["id"] != "1" || got[0].Score != 42 {
		t.Fatalf("hit = %+v, unexpected", got[0])
	}
}

func TestHitsFromTypesense_skipsEmptySnippets(t *testing.T) {
	t.Parallel()
	in := []typesenseHit{{
		Highlights: []struct {
			Field   string `json:"field"`
			Snippet string `json:"snippet"`
		}{
			{Field: "name", Snippet: "<em>shoe</em>"},
			{Field: "brand", Snippet: ""},
		},
	}}
	got := hitsFromTypesense(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if len(got[0].Highlight) != 1 || got[0].Highlight["name"] != "<em>shoe</em>" {
		t.Fatalf("Highlight = %+v, want only the non-empty snippet", got[0].Highlight)
	}
	if _, ok := got[0].Highlight["brand"]; ok {
		t.Fatal("empty snippet should not appear in Highlight")
	}
}

func TestHitsFromTypesense_emptyInput(t *testing.T) {
	t.Parallel()
	got := hitsFromTypesense(nil)
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
