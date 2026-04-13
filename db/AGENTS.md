# Agent guide — db/

Database layer. Three subpackages, all opt-in via `golusoris.DB` (or imported individually):

| Subpackage | Purpose |
|---|---|
| `db/pgx` | `*pgxpool.Pool` fx module with retry + slow-query tracer |
| `db/migrate` | golang-migrate v4 runner with optional auto-up on fx Start |
| `db/sqlc` | runtime helpers for sqlc-generated queries (WithTx, MapError) |

`testutil/pg` (sibling, not under db/) boots a real Postgres via testcontainers
for integration tests against this layer.

## Conventions

- Config keys live under `db.*` (env: `APP_DB_*`).
- Wiring order: `Core` → `dbpgx.Module` → `dbmigrate.Module`. Migrate inherits the pgx DSN by default.
- Apps don't import `database/sql`. pgx is the only supported driver.
