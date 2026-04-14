# search/typesense

Typesense backend for `search.Backend` — covers both the Indexer and Searcher interfaces.

## Surface

- `typesense.NewBackend(Options)` → `*Backend`.
- `Options{URL, APIKey, HTTPClient}`.

## Notes

- Raw HTTP — no SDK. Targets Typesense's standard REST API.
- Indexing uses the JSONL `/documents/import?action=upsert` endpoint
  so repeated indexing is idempotent.
- `Query.Fields` maps to `query_by` (Typesense requires it; leaving
  empty sends an empty `query_by` which Typesense rejects).
- `Query.Filters` is translated to `filter_by` via the `:=` equality
  syntax (`brand:=nike && price:=100`). Use `RawFilter` for ranges
  (`price:>100`) or more complex predicates.
- `Offset` is converted to 1-indexed `page` using the request Limit
  (falls back to 10 when Limit is zero).
- `CreateCollection` treats HTTP 409 as idempotent success — matches
  Typesense's "collection already exists" response.
- `Hit.Score` carries Typesense's `text_match` score; `Highlight`
  maps `field → snippet`.
