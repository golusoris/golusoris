# Agent guide — db/migrate

Wraps golang-migrate v4 with the pgx/v5 database driver. Provides `*Migrator` via fx.

## Conventions

- Migrations are off-by-default at fx start. Set `db.migrate.auto=true` to run on Start, or call `Migrator.Up()` from a CLI command (preferred for production: run as init container or CI step).
- Source is `file://` by default at `migrations/`. For embedded migrations, override `Options` via `fx.Replace(migrate.Options{Auto: true}.WithFS(myEmbedFS))`.
- `pgxToMigrateURL` rewrites `postgres://` → `pgx5://` so users keep their normal pgx DSN.

## Pinned upstream

- `golang-migrate/migrate/v4` v4.19.1
- pgx/v5 driver: `github.com/golang-migrate/migrate/v4/database/pgx/v5`

## Don't

- Don't import `database/sql/driver/postgres` — we use pgx5 only.
- Don't auto-rollback in production — `Down()` is a manual escape hatch.
