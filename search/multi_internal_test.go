// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package search

import "testing"

type typedNilSearcher struct{ Searcher }

func TestNewMultiSearcherIgnoresTypedNilBackends(t *testing.T) {
	t.Parallel()
	var backend *typedNilSearcher
	searcher := NewMultiSearcher([]Searcher{backend})
	if len(searcher.backends) != 0 {
		t.Fatalf("retained backends = %d, want 0", len(searcher.backends))
	}
}

func TestNewMultiSearcherIgnoresNilOption(t *testing.T) {
	t.Parallel()
	if searcher := NewMultiSearcher(nil, nil); searcher == nil {
		t.Fatal("NewMultiSearcher returned nil")
	}
}
