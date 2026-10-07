<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# search/pgfts

Postgres full-text search backend for `search.Searcher`. Apps that
already run Postgres get real search without extra service.

## Surface

- `pgfts.New(*pgxpool.Pool, Options)` → `*Searcher`.
- `Options{Table, VectorColumn, Language, Columns, RankColumn}`.

## Notes

- Searcher-only — no `Indexer`. Apps own their table shape, migrations,
 and INSERT/UPDATE flow. package is unopinionated on where  `tsvector` column comes from (GENERATED column, trigger, or
 application-side tsvector build).
- Expected baseline:

  ```sql
  CREATE TABLE <name> (
      id text PRIMARY KEY,
      content text NOT NULL,
      search_vec tsvector GENERATED ALWAYS AS
          (to_tsvector('english', content)) STORED
  );
  CREATE INDEX ON <name> USING GIN (search_vec);
  ```

- `Options.Table` pins table; when empty, `collection` argument
 to `Search` is used directly.
- `Options.VectorColumn` default `"search_vec"`.
- `Options.Language` default `"english"`; passed as  `::regconfig` parameter to `plainto_tsquery`.
- Query `Filters` are not supported — use `RawFilter` for trust-me
 SQL fragment appended to WHERE clause. Raw filters are  caller's responsibility to sanitise.
- Results are ordered by `ts_rank` DESC. `Hit.Score` carries raw
 rank.
- Identifier validation: table + column names allow only
 `[A-Za-z0-9_.]` — rejects injection attempts. Use schema-qualified
 names (`public.docs`) if needed.
