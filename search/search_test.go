// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package search_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/golusoris/golusoris/search"
)

func TestValidFilterField(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"name":           true,
		"product.name":   true,
		"unicode_字段":     true,
		"":               false,
		".name":          false,
		"name.":          false,
		"product..name":  false,
		"name-with-dash": false,
		"name space":     false,
	}
	for field, want := range tests {
		if got := search.ValidFilterField(field); got != want {
			t.Errorf("ValidFilterField(%q) = %v, want %v", field, got, want)
		}
	}
}

func TestMemorySearcher_basic(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()

	_ = s.CreateCollection(ctx, search.Schema{Name: "products"})
	_ = s.Index(ctx, "products", []search.Document{
		{"id": "1", "name": "Blue Sneaker", "brand": "Nike"},
		{"id": "2", "name": "Red Boot", "brand": "Adidas"},
		{"id": "3", "name": "Blue Boot", "brand": "Nike"},
	})

	results, err := s.Search(ctx, "products", search.Query{Q: "blue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Hits) != 2 {
		t.Fatalf("expected 2 hits for 'blue', got %d", len(results.Hits))
	}
	if results.Total != 2 {
		t.Fatalf("expected total=2, got %d", results.Total)
	}
}

func TestMemorySearcher_filter(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()

	_ = s.Index(ctx, "products", []search.Document{
		{"id": "1", "name": "Blue Sneaker", "brand": "Nike"},
		{"id": "2", "name": "Red Boot", "brand": "Adidas"},
	})

	results, err := s.Search(ctx, "products", search.Query{
		Q:       "*",
		Filters: map[string]any{"brand": "Nike"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Hits) != 1 {
		t.Fatalf("expected 1 hit for brand=Nike, got %d", len(results.Hits))
	}
}

func TestMemorySearcher_pagination(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()

	docs := make([]search.Document, 10)
	for i := range docs {
		docs[i] = search.Document{"id": string(rune('0' + i)), "name": "item"}
	}
	_ = s.Index(ctx, "items", docs)

	results, err := s.Search(ctx, "items", search.Query{Q: "*", Limit: 3, Offset: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Hits) != 3 {
		t.Fatalf("expected 3 hits (limit=3 offset=5 from 10), got %d", len(results.Hits))
	}
	if results.Total != 10 {
		t.Fatalf("expected total=10, got %d", results.Total)
	}
}

func TestMemorySearcher_delete(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()

	_ = s.Index(ctx, "items", []search.Document{
		{"id": "a", "name": "alpha"},
		{"id": "b", "name": "beta"},
	})
	_ = s.Delete(ctx, "items", []string{"a"})

	results, _ := s.Search(ctx, "items", search.Query{Q: "*"})
	if len(results.Hits) != 1 {
		t.Fatalf("expected 1 hit after delete, got %d", len(results.Hits))
	}
}

func TestMemorySearcher_matchAll(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()

	_ = s.Index(ctx, "things", []search.Document{
		{"id": "1", "v": "foo"},
		{"id": "2", "v": "bar"},
	})

	r, _ := s.Search(ctx, "things", search.Query{Q: "*"})
	if len(r.Hits) != 2 {
		t.Fatalf("wildcard should match all, got %d", len(r.Hits))
	}
}

func TestMemorySearcher_deleteCollection(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()
	_ = s.CreateCollection(ctx, search.Schema{Name: "col"})
	_ = s.Index(ctx, "col", []search.Document{{"id": "1"}})
	if err := s.DeleteCollection(ctx, "col"); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	// After deletion the collection is gone; search returns empty.
	r, _ := s.Search(ctx, "col", search.Query{Q: "*"})
	if len(r.Hits) != 0 {
		t.Fatalf("expected 0 hits after collection delete, got %d", len(r.Hits))
	}
}

func TestMemorySearcher_filterCompositeValues(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()
	doc := search.Document{
		"id":   "1",
		"tags": []string{"blue", "sale"},
		"meta": map[string]any{"tier": "gold"},
	}
	if err := s.Index(ctx, "products", []search.Document{doc}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := s.Search(ctx, "products", search.Query{
		Q: "*",
		Filters: map[string]any{
			"tags": []string{"blue", "sale"},
			"meta": map[string]any{"tier": "gold"},
		},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results.Hits) != 1 {
		t.Fatalf("composite filter hits = %d, want 1", len(results.Hits))
	}
}

func TestMemorySearcher_indexUpsertsAndIsolatesDocuments(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()
	first := search.Document{"id": "same", "name": "first"}
	if err := s.Index(ctx, "products", []search.Document{first}); err != nil {
		t.Fatalf("first Index: %v", err)
	}
	first["name"] = "caller-mutated"

	second := search.Document{"id": "same", "name": "second"}
	if err := s.Index(ctx, "products", []search.Document{second}); err != nil {
		t.Fatalf("second Index: %v", err)
	}
	second["name"] = "caller-mutated-again"

	results, err := s.Search(ctx, "products", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results.Hits) != 1 || results.Total != 1 {
		t.Fatalf("upsert result = %+v, want one document", results)
	}
	if got := results.Hits[0].Document["name"]; got != "second" {
		t.Fatalf("upserted name = %v, want second", got)
	}

	results.Hits[0].Document["name"] = "result-mutated"
	again, err := s.Search(ctx, "products", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if got := again.Hits[0].Document["name"]; got != "second" {
		t.Fatalf("stored name after result mutation = %v, want second", got)
	}
}

func TestMemorySearcher_honorsCanceledContext(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Search(ctx, "products", search.Query{Q: "*"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search error = %v, want context.Canceled", err)
	}
}

func TestMemorySearcher_searchDeleteRace(t *testing.T) {
	t.Parallel()
	s := search.NewMemorySearcher()
	ctx := context.Background()
	docs := make([]search.Document, 512)
	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = strconv.Itoa(i)
		docs[i] = search.Document{"id": ids[i], "name": "item"}
	}
	if err := s.Index(ctx, "items", docs); err != nil {
		t.Fatalf("seed Index: %v", err)
	}

	const rounds = 100
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range rounds {
			if _, err := s.Search(ctx, "items", search.Query{Q: "*"}); err != nil {
				t.Errorf("Search: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range rounds {
			if err := s.Delete(ctx, "items", ids); err != nil {
				t.Errorf("Delete: %v", err)
				return
			}
			if err := s.Index(ctx, "items", docs); err != nil {
				t.Errorf("Index: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}
