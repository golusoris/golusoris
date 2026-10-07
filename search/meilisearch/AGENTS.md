<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# search/meilisearch

Meilisearch backend for `search.Backend`.

## Surface

- `meilisearch.NewBackend(Options)` → `*Backend`.
- `Options{URL, APIKey, HTTPClient, MaxResponseBytes}`.

## Notes

- Raw HTTP — no SDK. `APIKey` is optional (Meili can run without master key).
- Injected clients are cloned; missing timeouts become 10s. Decoded responses
 default to 16 MiB maximum.
- `CreateCollection` creates index and, from schema's `Facet` /
 `Sort` hints, PUTs `filterable-attributes` / `sortable-attributes`.
- `Index` uses POST `/indexes/:name/documents` (upsert by `primaryKey`,
 which defaults to `"id"`).
- `Delete` uses `delete-batch` endpoint.
- `Query.Filters` is translated to Meili's filter DSL (`k = "v" AND …`).
 Use `RawFilter` for range/OR/NOT predicates.
- `Query.Offset` is passed through directly (Meili uses offset natively,
 unlike Typesense's page-based paging).
- `Query.SortBy` is comma-split into Meili's `sort` array. Use  `field:asc` or `field:desc` form Meili expects.
- `Total` comes from Meili's `estimatedTotalHits`. Use `finite-pagination`
 settings in production if exact totals matter.
