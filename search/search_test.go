package search_test

import (
	"context"
	"testing"

	"github.com/golusoris/golusoris/search"
)

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
