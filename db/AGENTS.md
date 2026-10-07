<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/

Database layer. Nine subpackages. `golusoris.DB` bundles `db/pgx` +
`db/migrate`; `golusoris.DBBun` bundles `db/bun`; remaining packages opt in
through their own module or direct import.

| Subpackage | Purpose |
|---|---|
| `db/pgx` | `*pgxpool.Pool` fx module with retry + slow-query tracer |
| `db/migrate` | golang-migrate v4 runner with optional auto-up on fx Start |
| `db/sqlc` | runtime helpers for sqlc-generated queries (WithTx, MapError) |
| `db/bun` | bun ORM fx module over the shared pgx pool |
| `db/cdc` | PostgreSQL logical-replication consumer + pgoutput decoder |
| `db/clickhouse` | ClickHouse OLAP client fx module |
| `db/geo` | PostGIS point codecs + Haversine helper |
| `db/sqlite` | pure-Go SQLite fx module |
| `db/timescale` | TimescaleDB hypertable, retention, and compression helpers |

`testutil/pg` (sibling, not under db/) boots real Postgres via testcontainers
for integration tests against this layer.

## Conventions

- Config keys live under `db.*` (env: `APP_DB_*`).
- Wiring order: `Core` → `dbpgx.Module` → `dbmigrate.Module`. Migrate inherits pgx DSN by default.
- Apps don't import `database/sql`. pgx is only supported driver.
