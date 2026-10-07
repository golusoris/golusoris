<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/bun/

Opt-in [uptrace/bun](https://bun.uptrace.dev) ORM module, alternative to hand-written queries in `db/sqlc`. It does **not** open its own connection — it
borrows `*pgxpool.Pool` from `db/pgx` (via `stdlib.OpenDBFromPool`), so app can mix bun and sqlc against one pool.

## Key surface

| Symbol | Purpose |
|---|---|
| `Module` | Provides `*bun.DB` over the db/pgx pool |
| `Options` | `verbose` (koanf, prefix `db.bun`) — install bun's debug query hook |
| `New(pool, opts, logger)` | Validate dependencies; build the `*bun.DB` directly (tests, custom wiring) |

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

`*bun.DB` borrows pool. Module closes its `database/sql` adapter before
`db/pgx` stops the shared pool. `stdlib.OpenDBFromPool` guarantees adapter
close does not close pool.

## Tests

`bun_test.go` has config-wiring unit test plus integration test
(`testutil/pg`) that runs raw query and builder query through pgdialect. integration test skips automatically when Docker is unavailable.

## Don't

- Direct `New` caller handles its error and closes bun adapter. Shared pgx pool remains open.
- Don't use this *and* expect `db/pgx`'s slow-query tracer on bun queries —  tracer wraps pgx path; bun goes through `database/sql`. Use `verbose` for
 bun-side query logging.
- Don't reach for bun where sqlc query already exists — pick one per query
 surface; both share pool, not query cache.
