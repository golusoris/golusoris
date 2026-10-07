<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/pg

Boots real Postgres container via testcontainers-go for tests that need genuine database. Docker is hard requirement (no fake/mock fallback).

## Conventions

- `pg.Start(t)` returns connected `*pgxpool.Pool`. container + pool are torn down via `t.Cleanup`. Each call gets its own container — tests are isolated.
- For tests that need only DSN (e.g. driving `db/migrate`), use `pg.DSN(t)`.
- For logical-replication tests (e.g. `db/cdc`), use `pg.StartReplication(t)` — it boots `wal_level=logical` container and returns `(pool, replicationDSN)`; DSN already carries `replication=database`.
- For TimescaleDB tests, use `pg.StartTimescale(t)` — immutable repository pin + `CREATE EXTENSION` before return.
- Default PostgreSQL + TimescaleDB references: `internal/testimages` authority.
- `Options.Image`: exact `tag@sha256:<64 lowercase hex>` required; mutable override fails before Docker access.
- Every start is bounded by `startTimeout` (3 min, image pull included) — sized for cold-cache CI ARC runners where all container packages pull at once. Keep it scalar constant (HISS-02).
- Image updates: change `internal/testimages` + `.github/testcontainers-images.txt`; Renovate manager + parity test enforce both copies.
- Ryuk: immutable repository pin; helper overrides testcontainers default.
- Every helper takes slot from `testutil/internal/startgate` before booting, so at most 2 containers start at once per test binary; parallel tests beyond that queue instead of timing out.

## Key surface

| Helper | Returns | Use when |
|---|---|---|
| `Start(t, …)` | `*pgxpool.Pool` | plain Postgres |
| `DSN(t, …)` | `string` | you need the raw connection string |
| `StartReplication(t, …)` | `(*pgxpool.Pool, string)` | logical replication / CDC |
| `StartTimescale(t, …)` | `*pgxpool.Pool` | TimescaleDB hypertables |

## Don't

- Don't share containers across test files via package-level vars — use TestMain or sync.Once helper if you genuinely need to amortize startup cost.
- Don't `t.Skip()` when Docker is missing — CI without Docker is CI bug.
