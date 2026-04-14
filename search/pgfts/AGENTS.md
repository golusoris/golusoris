# search/pgfts

Postgres full-text search backend for `search.Searcher`. Apps that
already run Postgres get real search without an extra service.

## Surface

- `pgfts.New(*pgxpool.Pool, Options)` → `*Searcher`.
- `Options{Table, VectorColumn, Language, Columns, RankColumn}`.

## Notes

- Searcher-only — no `Indexer`. Apps own their table shape, migrations,
  and INSERT/UPDATE flow. The package is unopinionated on where the
  `tsvector` column comes from (GENERATED column, trigger, or
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

- `Options.Table` pins the table; when empty, the `collection` argument
  to `Search` is used directly.
- `Options.VectorColumn` default `"search_vec"`.
- `Options.Language` default `"english"`; passed as the
  `::regconfig` parameter to `plainto_tsquery`.
- Query `Filters` are not supported — use `RawFilter` for a trust-me
  SQL fragment appended to the WHERE clause. Raw filters are the
  caller's responsibility to sanitise.
- Results are ordered by `ts_rank` DESC. `Hit.Score` carries the raw
  rank.
- Identifier validation: table + column names allow only
  `[A-Za-z0-9_.]` — rejects injection attempts. Use schema-qualified
  names (`public.docs`) if needed.
