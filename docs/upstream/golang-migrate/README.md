# golang-migrate/migrate/v4 — v4.18.3 snapshot

Pinned: **v4.18.3**
Source: https://pkg.go.dev/github.com/golang-migrate/migrate/v4@v4.18.3

## Usage

```go
import (
    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
    _ "github.com/golang-migrate/migrate/v4/source/file"
)

m, err := migrate.New("file://migrations", "pgx5://user:pass@host/db")
err = m.Up()                // apply all pending
err = m.Steps(2)            // apply N migrations
err = m.Down()              // rollback all
err = m.Migrate(3)          // migrate to version 3
version, dirty, err := m.Version()
```

## Embedded FS source

```go
import "github.com/golang-migrate/migrate/v4/source/iofs"

//go:embed migrations/*.sql
var migrationsFS embed.FS

d, _ := iofs.New(migrationsFS, "migrations")
m, _ := migrate.NewWithSourceInstance("iofs", d, dbURL)
```

## Migration file naming

```
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

- `db/migrate/` — fx module; runs `m.Up()` on `OnStart` (configurable: auto-up or manual).
- Migration files live in `db/migrations/` per app (embedded via `embed.FS`).

## Links

- Changelog: https://github.com/golang-migrate/migrate/blob/master/CHANGELOG.md
