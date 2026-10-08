<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# golang-migrate/migrate/v4 — v4.20.1 snapshot

Pinned: **v4.20.1**
Source: [tagged source](https://github.com/golang-migrate/migrate/tree/v4.20.1)

## Usage

```go
import (
    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
    _ "github.com/golang-migrate/migrate/v4/source/file"
)

m, err := migrate.New("file://migrations", "pgx5://user:pass@host/db")
if err != nil {
    return fmt.Errorf("open migrations: %w", err)
}
err = m.Up()                // apply all pending
err = m.Steps(2)            // apply N migrations
err = m.Down()              // rollback all
err = m.Migrate(3)          // migrate to version 3
version, dirty, err := m.Version()

sourceErr, databaseErr := m.Close()
if closeErr := errors.Join(sourceErr, databaseErr); closeErr != nil {
    return fmt.Errorf("close migrations: %w", closeErr)
}
```

## Embedded FS source

```go
import "github.com/golang-migrate/migrate/v4/source/iofs"

//go:embed migrations/*.sql
var migrationsFS embed.FS

d, err := iofs.New(migrationsFS, "migrations")
if err != nil {
    return fmt.Errorf("open embedded migrations: %w", err)
}
m, err := migrate.NewWithSourceInstance("iofs", d, dbURL)
if err != nil {
    return fmt.Errorf("open migration database: %w", err)
}
```

## Migration file naming

```text
000001_create_users.up.sql
000001_create_users.down.sql
000002_add_email_index.up.sql
000002_add_email_index.down.sql
```

## Error handling

```go
if err != nil && !errors.Is(err, migrate.ErrNoChange) {
    return fmt.Errorf("migrate: %w", err)
}
```

## golusoris usage

- `db/migrate/` — Fx module; runs `m.Up()` on `OnStart` (configurable:
  automatic or manual).
- Migration files live in `db/migrations/` per app (embedded via `embed.FS`).

## Links

- [Package documentation](https://pkg.go.dev/github.com/golang-migrate/migrate/v4@v4.20.1)
