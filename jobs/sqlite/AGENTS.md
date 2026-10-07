<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — jobs/sqlite

River queue on SQLite (riversqlite) for standalone single-binary mode.
Same `jobs.Options` (`jobs.*` keys), same worker registry, same drain.

## Conventions

- fx: `dbsqlite.Module` (provides `*sql.DB`) + `sqlite.Module` +
 optional `jobs.MetricsModule`. Module provides `*jobs.ClientSQL`,
 `*jobs.Workers`, `jobs.Inserter`, `jobs.DepthQuerier`.
- fx Start: `Migrate` (River SQLite migrations, idempotent, 30s bound)
 then client Start; fx Stop: `jobs.Drain` soft -> hard.
- App code enqueues via `jobs.Inserter` (Insert/InsertMany) -> same code
 on Postgres (cluster) + SQLite (standalone).
- Tx inserts stay driver-typed: `(*jobs.ClientSQL).InsertTx(ctx, *sql.Tx, ...)`
 vs `(*jobs.Client).InsertTx(ctx, pgx.Tx, ...)`. Atomic enqueue with app
 writes needs concrete client.
- Upstream advice: `db.SetMaxOpenConns(1)`; db/sqlite default pool (4,
 WAL, busy_timeout 5s) passes `TestModule_fxLifecycle`.
- `DepthQuerier`: one grouped `river_job` read; tenant via
 `json_extract(metadata, '$."key"')`; keys with `"` or `\` rejected.

## Don't

- Don't share one SQLite file across processes for jobs — single writer,
 no LISTEN/NOTIFY; use jobs (Postgres) for multi-replica.
- Don't call `JobListParams.Metadata` — River rejects it on SQLite.
