# Agent guide — ai/vector/

pgvector helpers for storing and querying vector embeddings using the
PostgreSQL `pgvector` extension. Wraps `pgvector/pgvector-go` types for use
with pgx/v5.

## Core types

| Symbol | Purpose |
|---|---|
| `Vector` | Alias for `pgvec.Vector` — use in pgx query params + sqlc structs |
| `From([]float32)` | Convert embedding slice to `Vector` |
| `Metric` | `Cosine` (`<=>`) · `L2` (`<->`) · `InnerProduct` (`<#>`) |
| `SimilaritySearch(ctx, pool, SearchQuery)` | Nearest-neighbour query |
| `RegisterTypes(ctx, pool)` | Register pgvector OIDs on the pgx pool |

## Usage

```go
// Schema:
// CREATE TABLE items (id TEXT, embedding vector(1536));
// CREATE INDEX ON items USING hnsw (embedding vector_cosine_ops);

// Store:
_, err := pool.Exec(ctx,
    "INSERT INTO items (id, embedding) VALUES ($1, $2)",
    id, vector.From(embedding))

// Query:
ids, err := vector.SimilaritySearch(ctx, pool, vector.SearchQuery{
    Table: "items", IDColumn: "id", VecColumn: "embedding",
    Embedding: queryVec, Limit: 10, Metric: vector.Cosine,
})
```

## Don't

- Don't forget to call `RegisterTypes` at startup — pgx won't know how to
  encode/decode `vector` columns otherwise.
- Don't pass user-controlled strings as `Table` or `IDColumn` — SQL injection.
- Don't store embeddings without an HNSW or IVFFlat index on large tables.
