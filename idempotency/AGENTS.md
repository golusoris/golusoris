<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/

HTTP middleware enforcing Idempotency-Key header
(draft-ietf-httpapi-idempotency-key-header). First request for scoped key runs
handler, stores bounded response; completed retry replays it without handler.

## Core types

| Type | Purpose |
| --- | --- |
| `CachedResponse` | Stored outcome: StatusCode, Header, Body |
| `Store` | Atomic fingerprinted `Claim` + token-bound `Commit` / `Release` |
| `Sweeper` | `Sweep(ctx, limit)` deletes ≤ limit expired records (cap `MaxSweepBatch` 10000) |
| `MemoryStore` | one process / tests; implements `Sweeper` |
| `PostgresStore` | shared across replicas; table `golusoris_idempotency_keys` from `MigrationsFS`; `Sweeper` |
| `RedisStore` | shared across replicas (Redis/Valkey); one hash per key, Lua claim/commit/release, server PX expiry |
| `SQLiteStore` | standalone node over `*sql.DB`; `EnsureSchema(ctx)` creates table; `Sweeper` |
| `Options` / `Middleware(store, opts)` | HTTP; wraps POST/PUT/PATCH/DELETE |
| `Module` | fx: Store + middleware + sweeper |

## Behaviour

- Scope: method + host + canonical target + tenancy ID + optional principal.
- Fingerprint: content type + body.
- In flight: 409. Other payload: 422. Oversized request: 413. Store failure: 500.
- Not stored (key released, retry runs again): 5xx; oversized responses.
- Missing key: pass through; `Required` -> 400.
- Expiry: memory/postgres/sqlite use injected clock (replica skew shifts expiry);
  redis uses server PX. Expired in-flight reservation -> next claim takes over, old token loses.

## fx wiring

```go
fx.New(
    golusoris.Core,
    golusoris.DB,           // *pgxpool.Pool when idempotency.store=postgres
    golusoris.Idempotency,  // Store + middleware.Middleware + sweeper
)
```

Backend client comes from graph: postgres -> `*pgxpool.Pool` (apply `MigrationsFS`
via db/migrate), redis -> `rueidis.Client` (cache/redis), sqlite -> `*sql.DB`
(db/sqlite; module runs `EnsureSchema`). Missing client -> start error.

## Config keys (prefix `idempotency`)

| Key | Default | Purpose |
| --- | --- | --- |
| `store` | `memory` | `memory` / `postgres` / `redis` / `sqlite` |
| `ttl` | `24h` | reservation + replay retention |
| `required` / `header` | `false` / `Idempotency-Key` | reject keyless / key header |
| `max_request_body` / `max_response_body` | 1 MiB | fingerprint / replay bounds |
| `redis.prefix` | `golusoris:idempotency:` | key namespace |
| `sweep.interval` | `1m` | GC period; `0` disables |
| `sweep.batch` / `sweep.timeout` | `1000` / `10s` | rows + deadline per batch; ≤ 16 batches per tick |

## Don't

- Don't use `MemoryStore` across replicas — each process executes once.
- Don't set `Required: true` on endpoints that dedupe via unique DB constraints.
- Don't skip `MigrationsFS` for postgres — claims fail without table.
