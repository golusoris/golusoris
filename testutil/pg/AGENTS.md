<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/pg

Boots a real Postgres container via testcontainers-go for tests that need a
genuine database. Docker is a hard requirement (no fake/mock fallback).

## Conventions

- `pg.Start(t)` returns a connected `*pgxpool.Pool`. The container + pool are torn down via `t.Cleanup`. Each call gets its own container — tests are isolated.
- For tests that need only the DSN (e.g. driving `db/migrate`), use `pg.DSN(t)`.
- For logical-replication tests (e.g. `db/cdc`), use `pg.StartReplication(t)` — it boots a `wal_level=logical` container and returns `(pool, replicationDSN)`; the DSN already carries `replication=database`.
- For TimescaleDB tests, use `pg.StartTimescale(t)` — it boots the pinned `timescale/timescaledb:2.30.0-pg17` image and `CREATE EXTENSION`s timescaledb before returning the pool.
- Default image is `postgres:17-alpine`. Override via `Options.Image`.
- Every start is bounded by `startTimeout` (3 min, image pull included) — sized for the cold-cache CI ARC runners where all container packages pull at once. Keep it a scalar constant (HISS-02).
- Image tags are pinned; when you bump one, update the "Pre-pull testcontainers images" list in `.github/workflows/ci.yml` so CI still starts warm.

## Key surface

| Helper | Returns | Use when |
|---|---|---|
| `Start(t, …)` | `*pgxpool.Pool` | plain Postgres |
| `DSN(t, …)` | `string` | you need the raw connection string |
| `StartReplication(t, …)` | `(*pgxpool.Pool, string)` | logical replication / CDC |
| `StartTimescale(t, …)` | `*pgxpool.Pool` | TimescaleDB hypertables |

## Don't

- Don't share containers across test files via package-level vars — use TestMain or a sync.Once helper if you genuinely need to amortize startup cost.
- Don't `t.Skip()` when Docker is missing — a CI without Docker is a CI bug.
