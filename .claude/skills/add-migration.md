Create a timestamped golang-migrate up/down SQL migration pair.

## Task

Create a migration for: `$ARGUMENTS`

## Steps

1. **Generate the filename** with the current Unix timestamp:

```sh
ts=$(date +%s)
name="<snake_case_description>"
touch db/migrations/${ts}_${name}.up.sql
touch db/migrations/${ts}_${name}.down.sql
```

2. **Write the up migration** — DDL only (CREATE TABLE, ADD COLUMN, CREATE INDEX, etc.):
   - Always use `IF NOT EXISTS` for CREATE TABLE / CREATE INDEX.
   - Never include DML (INSERT/UPDATE) in migrations — use a separate seeder.
   - Add a comment at the top: `-- Migration: <description>`

3. **Write the down migration** — the exact inverse:
   - DROP TABLE / DROP COLUMN / DROP INDEX as needed.
   - `IF EXISTS` variants to make down idempotent.

4. **Verify** with `go run github.com/golang-migrate/migrate/v4/cmd/migrate@latest`:

```sh
migrate -database "$POSTGRES_DSN" -path db/migrations up 1
migrate -database "$POSTGRES_DSN" -path db/migrations down 1
```

## Rules

- One logical change per migration pair.
- Never modify an existing migration that has been applied to any environment.
- Column renames: add new column → backfill → drop old (three separate migrations).
- Always test both up and down before committing.
