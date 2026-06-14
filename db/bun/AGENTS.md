# Agent guide — db/bun/

Opt-in [uptrace/bun](https://bun.uptrace.dev) ORM module, an alternative to the
hand-written queries in `db/sqlc`. It does **not** open its own connection — it
borrows the `*pgxpool.Pool` from `db/pgx` (via `stdlib.OpenDBFromPool`), so an
app can mix bun and sqlc against one pool.

## Key surface

| Symbol | Purpose |
|---|---|
| `Module` | Provides `*bun.DB` over the db/pgx pool |
| `Options` | `verbose` (koanf, prefix `db.bun`) — install bun's debug query hook |
| `New(pool, opts, logger)` | Build the `*bun.DB` directly (tests, custom wiring) |

## Wiring

```go
fx.New(
    golusoris.Core,
    golusoris.DB,     // *pgxpool.Pool
    golusoris.DBBun,  // *bun.DB over that pool
    fx.Invoke(func(db *bun.DB) error { /* db.NewSelect()… */ return nil }),
)
```

## Lifecycle

The `*bun.DB` borrows the pool — **db/pgx owns the connection lifecycle**. The
module deliberately registers no close hook; closing the bun.DB would tear down
the shared pool. Let `golusoris.DB` stop the pool on shutdown.

## Tests

`bun_test.go` has a config-wiring unit test plus an integration test
(`testutil/pg`) that runs a raw query and a builder query through pgdialect. The
integration test skips automatically when Docker is unavailable.

## Don't

- Don't call `db.Close()` — it closes the shared pool that db/pgx owns.
- Don't use this *and* expect `db/pgx`'s slow-query tracer on bun queries — the
  tracer wraps the pgx path; bun goes through `database/sql`. Use `verbose` for
  bun-side query logging.
- Don't reach for bun where a sqlc query already exists — pick one per query
  surface; both share the pool, not a query cache.
