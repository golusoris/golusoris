# Agent guide — search/

Provider-agnostic full-text and vector search abstraction.

## Core types

| Type | Purpose |
|---|---|
| `Indexer` | `CreateCollection / DeleteCollection / Index / Delete` |
| `Searcher` | `Search(ctx, collection, Query) Results` |
| `Backend` | `Indexer + Searcher` combined |
| `Query` | `Q`, `Fields`, `Filters`, `RawFilter`, `SortBy`, `Limit`, `Offset` |
| `Results` | `Hits []Hit`, `Total int64` |
| `MemorySearcher` | In-memory backend; case-insensitive substring match; for tests |

## Planned backends (sub-packages)

| Sub-package | Backend |
|---|---|
| `search/typesense/` | typesense-go/v2 |
| `search/meilisearch/` | meilisearch-go |
| `search/pgfts/` | Postgres `tsvector` / `tsquery` |

## Usage

```go
var s search.Backend = search.NewMemorySearcher() // swap for typesense in prod
_ = s.CreateCollection(ctx, search.Schema{Name: "products", Fields: [...]})
_ = s.Index(ctx, "products", docs)
results, _ := s.Search(ctx, "products", search.Query{Q: "blue sneaker", Limit: 20})
```

## Don't

- Don't use `MemorySearcher` in production — it's O(n) linear scan.
- Don't put unvalidated user input directly in `RawFilter` on SQL backends.
