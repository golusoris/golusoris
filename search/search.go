// Package search provides a provider-agnostic full-text and vector search
// abstraction. Backends (Typesense, Meilisearch, OpenSearch, Postgres FTS)
// implement the [Indexer] and [Searcher] interfaces. Use [MemorySearcher]
// for tests and local development.
//
// Usage:
//
//	var s search.Searcher = typesenseBackend // or memory, meilisearch, …
//	results, err := s.Search(ctx, "products", search.Query{
//	    Q:       "blue sneaker",
//	    Filters: search.Filters{"brand": "nike"},
//	    Limit:   20,
//	})
package search

import (
	"context"
	"strings"
	"sync"
)

// Document is a key-value map representing a single indexed record.
// The field named "id" (or the collection's configured id field) is used
// as the primary key.
type Document map[string]any

// FieldType describes the type of a schema field.
type FieldType string

// Schema field type constants.
const (
	FieldTypeString  FieldType = "string"
	FieldTypeInt     FieldType = "int32"
	FieldTypeFloat   FieldType = "float"
	FieldTypeBool    FieldType = "bool"
	FieldTypeStrings FieldType = "string[]"
)

// Field is one column in a collection schema.
type Field struct {
	Name  string
	Type  FieldType
	Facet bool // enable faceted filtering
	Sort  bool // enable sorting on this field
}

// Schema describes a search collection.
type Schema struct {
	Name   string
	Fields []Field
	// DefaultSortField is sorted by when no explicit sort is requested.
	DefaultSortField string
}

// Query carries search parameters.
type Query struct {
	// Q is the full-text query string. "*" means match-all.
	Q string
	// Fields to search in. Empty = all searchable fields.
	Fields []string
	// Filters maps field names to required values (equality). For
	// range/complex filters, backends accept a raw filter string via
	// RawFilter.
	Filters map[string]any
	// RawFilter is a backend-specific filter expression. Takes precedence
	// over Filters when non-empty.
	RawFilter string
	// SortBy is a field name + " asc" or " desc".
	SortBy string
	// Limit and Offset control pagination.
	Limit  int
	Offset int
}

// Hit is a single search result.
type Hit struct {
	Document  Document
	Score     float64           // relevance score; semantics are backend-dependent
	Highlight map[string]string // field → highlighted snippet
}

// Results is the response from a search query.
type Results struct {
	Hits  []Hit
	Total int64
	Page  int
	Took  int // milliseconds (may be 0 for backends that don't report it)
}

// Indexer manages collections and documents.
type Indexer interface {
	CreateCollection(ctx context.Context, schema Schema) error
	DeleteCollection(ctx context.Context, name string) error
	// Index upserts documents into collection. Large batches should be
	// chunked by the caller.
	Index(ctx context.Context, collection string, docs []Document) error
	Delete(ctx context.Context, collection string, ids []string) error
}

// Searcher executes queries.
type Searcher interface {
	Search(ctx context.Context, collection string, q Query) (Results, error)
}

// Backend combines both interfaces.
type Backend interface {
	Indexer
	Searcher
}

// --- MemorySearcher ---

// MemorySearcher is a naive in-memory Backend for tests and local dev.
// It performs case-insensitive substring matching on all string fields.
// Not suitable for production.
type MemorySearcher struct {
	mu          sync.RWMutex
	collections map[string][]Document
}

// NewMemorySearcher returns an empty MemorySearcher.
func NewMemorySearcher() *MemorySearcher {
	return &MemorySearcher{collections: map[string][]Document{}}
}

// CreateCollection implements [Searcher].
func (m *MemorySearcher) CreateCollection(_ context.Context, schema Schema) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.collections[schema.Name]; !ok {
		m.collections[schema.Name] = nil
	}
	return nil
}

// DeleteCollection implements [Searcher].
func (m *MemorySearcher) DeleteCollection(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.collections, name)
	return nil
}

// Index implements [Searcher].
func (m *MemorySearcher) Index(_ context.Context, collection string, docs []Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.collections[collection] = append(m.collections[collection], docs...)
	return nil
}

// Delete implements [Searcher].
func (m *MemorySearcher) Delete(_ context.Context, collection string, ids []string) error {
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	docs := m.collections[collection]
	kept := docs[:0]
	for _, d := range docs {
		if id, _ := d["id"].(string); id != "" {
			if _, remove := idSet[id]; remove {
				continue
			}
		}
		kept = append(kept, d)
	}
	m.collections[collection] = kept
	return nil
}

// Search implements [Searcher].
func (m *MemorySearcher) Search(_ context.Context, collection string, q Query) (Results, error) {
	m.mu.RLock()
	docs := m.collections[collection]
	m.mu.RUnlock()

	qLower := strings.ToLower(q.Q)
	matchAll := qLower == "" || qLower == "*"

	var hits []Hit
	for _, doc := range docs {
		if !matchAll && !docContains(doc, qLower) {
			continue
		}
		if !filterMatch(doc, q.Filters) {
			continue
		}
		hits = append(hits, Hit{Document: doc})
	}

	total := int64(len(hits))

	// Apply offset + limit.
	if q.Offset > 0 && q.Offset < len(hits) {
		hits = hits[q.Offset:]
	} else if q.Offset >= len(hits) {
		hits = nil
	}
	if q.Limit > 0 && len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}

	return Results{Hits: hits, Total: total}, nil
}

func docContains(doc Document, q string) bool {
	for _, v := range doc {
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

func filterMatch(doc Document, filters map[string]any) bool {
	for k, want := range filters {
		got, ok := doc[k]
		if !ok {
			return false
		}
		if got != want {
			return false
		}
	}
	return true
}
