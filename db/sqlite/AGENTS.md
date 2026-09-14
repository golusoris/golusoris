<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/sqlite

Embedded SQLite via **modernc.org/sqlite** (pure Go, CGO-free — static
binaries, easy cross-compiles). For local state in CLIs, edge agents, and
single-node services. Multi-writer or networked → `db/pgx`. Capability key:
`db.sqlite`; replaces direct `modernc.org/sqlite` / `mattn/go-sqlite3` use.

## fx wiring

```go
fx.New(golusoris.Core, sqlite.Module, fx.Invoke(func(db *sql.DB) { … }))
```

## Config keys (prefix `db.sqlite`)

| Key | Default | Purpose |
|---|---|---|
| `path` | **required** | file path or `:memory:` |
| `read_only` | `false` | open with `mode=ro` |
| `busy_timeout` | `5s` | wait on a locked database before `SQLITE_BUSY` |
| `max_open_conns` | `4` | pool bound (`:memory:` is pinned to 1 so every statement sees the same DB) |
| `disable_wal` | `false` | WAL (`journal_mode=WAL`) is on by default — concurrent readers while one writer proceeds |
| `disable_foreign_keys` | `false` | declared FKs are enforced by default (SQLite itself defaults to off) |
| `pragmas` | `[]` | extra `name(value)` pragmas appended to the DSN |

## Key API

| Symbol | Purpose |
|---|---|
| `Open(ctx, Options, logger)` | open + ping; caller owns the `*sql.DB` |
| `Options.DSN()` | the `file:…?_pragma=…` connection string |
| `Module` | provides `*sql.DB`, closes on fx stop |
| `ErrMissingPath` | no path configured |

## Don't

- Don't run migrations here; wire `db/migrate` with the sqlite driver source,
  or ship schema with `CREATE TABLE IF NOT EXISTS` in an `fx.Invoke`.
- Don't raise `max_open_conns` expecting write parallelism — SQLite has one
  writer; WAL only parallelises readers.
- Don't use `:memory:` for anything but tests and throwaway caches.
