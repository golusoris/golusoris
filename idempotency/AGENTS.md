<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/

HTTP middleware + gRPC unary interceptor enforcing idempotency keys
(draft-ietf-httpapi-idempotency-key-header). First call for scoped key runs
handler, stores bounded outcome; completed retry replays it without handler.

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
| `GRPCOptions` / `UnaryServerInterceptor(store, opts)` | gRPC unary; metadata key `idempotency-key` |
| `StreamServerInterceptor(opts)` | rejects keyed streams (`InvalidArgument`); keyless pass |
| `Module` / `GRPCModule` | fx: Store + middleware + sweeper / interceptor on `grpc.Module` server |

## Behaviour

- Scope: HTTP = method + host + canonical target + tenancy ID + optional principal.
  gRPC = full method + tenancy ID + optional `GRPCScopeFunc`; keyspace `grpc:v1:`.
- Fingerprint: HTTP = content type + body; gRPC = message name + deterministic proto bytes.
- In flight: HTTP 409 / gRPC `Aborted`. Other payload: HTTP 422 / gRPC `FailedPrecondition`.
- Oversized request: HTTP 413 / gRPC `ResourceExhausted`. Store failure: HTTP 500 / gRPC `Unavailable`.
- Not stored (key released, retry runs again): HTTP 5xx; gRPC codes outside
  OK, InvalidArgument, NotFound, AlreadyExists, PermissionDenied,
  FailedPrecondition, OutOfRange, Unauthenticated; non-status errors; oversized responses.
- gRPC replay: message stored as `anypb.Any`, decoded via global proto registry;
  status stored as `google.rpc.Status`. Response headers/trailers not replayed.
- Missing key: pass through; `Required` -> HTTP 400 / gRPC `InvalidArgument` (unary only).
- Expiry: memory/postgres/sqlite use injected clock (replica skew shifts expiry);
  redis uses server PX. Expired in-flight reservation -> next claim takes over, old token loses.

## fx wiring

```go
fx.New(
    golusoris.Core,
    golusoris.DB,           // *pgxpool.Pool when idempotency.store=postgres
    golusoris.Idempotency,  // Store + middleware.Middleware + sweeper
    grpc.Module,            // golusoris/grpc server
    idempotency.GRPCModule, // optional unary interceptor
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
| `required` / `header` | `false` / `Idempotency-Key` | HTTP |
| `max_request_body` / `max_response_body` | 1 MiB | fingerprint / replay bounds |
| `redis.prefix` | `golusoris:idempotency:` | key namespace |
| `sweep.interval` | `1m` | GC period; `0` disables |
| `sweep.batch` / `sweep.timeout` | `1000` / `10s` | rows + deadline per batch; ≤ 16 batches per tick |
| `grpc.metadata` / `grpc.required` | `idempotency-key` / `false` | gRPC |

## Don't

- Don't use `MemoryStore` across replicas — each process executes once.
- Don't set `Required: true` on endpoints that dedupe via unique DB constraints.
- Don't send idempotency key on streaming RPCs — streams cannot replay.
- Don't skip `MigrationsFS` for postgres — claims fail without table.
