<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/migrate

Wraps golang-migrate v4. Provides `*Migrator` via fx: `Module` / `New` for
PostgreSQL (pgx/v5 driver), `db/migrate/sqlite` for SQLite, `Open` for any
other golang-migrate database URL whose driver program registers.

## Conventions

- Migrations are off-by-default at fx start. Set `db.migrate.auto=true` to run on Start, or call `Migrator.Up()` from CLI command (preferred for production: run as init container or CI step).
- Source is `file://` by default at `migrations/`. For embedded migrations, override `Options` via `fx.Replace(migrate.Options{Auto: true}.WithFS(myEmbedFS))`.
- Filesystem source URL escapes reserved path bytes; `?` and `#` remain filename bytes.
- `migrate.go` blank-imports golang-migrate `source/file`: missing import -> `Options.Path`
  fails "unknown driver 'file'" (before #643 fix: never registered).
- golang-migrate file parser joins URL host + path. Windows drive path ->
  `file://C:/dir`, not RFC 8089 `file:///C:/dir`; UNC -> `file:////server/share`.
  `fileSourceURLFor(path, windows)` pure; tests run Windows rows on every OS.
- `pgxToMigrateURL` rewrites `postgres://` → `pgx5://` so users keep their normal pgx DSN.
- `New` applies `db.ssl.*` + `db.password_file` to migrator URL via `dbpgx.Options.ConnString`
  (pool precedence; files read once at construction, no rotation). No other `db.*` option applies.
  Keyword/value DSN refused; `db.ssl.*` file path with space refused (driver re-encodes space as `+`). #771.
- Driver-specific modules reuse `LoadOptions` (config `db.migrate`) and `Bind`
  (Up on start when `auto`, best-effort Close on stop); never copy them.
- SQLite lives in `db/migrate/sqlite`, own package, so PostgreSQL-only
  programs do not link SQLite driver. Opens own connection with `db/sqlite`
  pragmas (`URL`); refuses `:memory:` and read-only databases.

## Pinned upstream

- `golang-migrate/migrate/v4` v4.20.1 (`go.mod`)
- pgx/v5 driver: `github.com/golang-migrate/migrate/v4/database/pgx/v5`
- SQLite driver (`db/migrate/sqlite`): `github.com/golang-migrate/migrate/v4/database/sqlite` (modernc.org/sqlite)

## Don't

- Don't import `database/sql/driver/postgres` — we use pgx5 only.
- Don't auto-rollback in production — `Down()` is manual escape hatch.
