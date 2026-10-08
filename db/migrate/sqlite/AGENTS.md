<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/migrate/sqlite

golang-migrate runner for SQLite on pure-Go `database/sqlite` driver
(modernc.org/sqlite, same driver as `db/sqlite`). Returns `*migrate.Migrator`,
same type as `db/migrate`. Capability key: `db.migrate.sqlite`.

## fx wiring

```go
fx.New(golusoris.Core, dbsqlite.Module, migratesqlite.Module,
    fx.Replace(dbmigrate.Options{Auto: true}.WithFS(migrationsFS)))
```

Config: `db.migrate.path`, `db.migrate.auto` (as `db/migrate`) and
`db.sqlite.*` (path, pragmas). `db.migrate.dsn` is not read.

## Key API

| Symbol | Purpose |
| --- | --- |
| `New(opts, sqliteOpts, logger)` | Migrator for `sqliteOpts` database |
| `URL(sqliteOpts)` | `sqlite://<path>?<db/sqlite pragmas>` |
| `Module` | provides `*dbmigrate.Migrator`; Up on start when `auto` |
| `ErrMemoryDatabase`, `ErrReadOnlyDatabase` | refused targets; missing path -> `dbsqlite.ErrMissingPath` |

## Don't

- Don't wire `Module` next to `db/migrate.Module`: both provide the Migrator.
- Don't point it at `:memory:`: migrator's own connection would migrate
  private database.
- Don't build URL by hand: `URL` keeps `db/sqlite` pragmas.
