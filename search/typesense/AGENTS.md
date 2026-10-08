<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# search/typesense

Typesense backend for `search.Backend` — covers both Indexer and Searcher interfaces.

## Surface

- `typesense.NewBackend(Options)` → `*Backend`.
- `Options{URL, APIKey, HTTPClient, MaxResponseBytes}`.

## Notes

- Raw HTTP — no SDK. Targets Typesense's standard REST API.
- Injected clients are cloned; missing timeouts become 10s. Decoded responses
 default to 16 MiB maximum.
- Indexing uses JSONL `/documents/import?action=upsert` endpoint
 so repeated indexing is idempotent.
- `Query.Fields` maps to `query_by` (Typesense requires it; leaving
 empty sends empty `query_by` which Typesense rejects).
- `Query.Filters` is translated to `filter_by` via `:=` equality
 syntax (`brand:=nike && price:=100`). Use `RawFilter` for ranges
 (`price:>100`) or more complex predicates.
- `Offset` is converted to 1-indexed `page` using request Limit
 (falls back to 10 when Limit is zero).
- `CreateCollection` treats HTTP 409 as idempotent success — matches
 Typesense's "collection already exists" response.
- `Hit.Score` carries Typesense's `text_match` score; `Highlight`
 maps `field → snippet`.
