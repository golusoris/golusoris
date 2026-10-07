<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

Create timestamped golang-migrate up/down SQL migration pair.

## Task

Create migration for: `$ARGUMENTS`

## Steps

1. **Generate filename** with current Unix timestamp:

```sh
ts=$(date +%s)
name="<snake_case_description>"
touch db/migrations/${ts}_${name}.up.sql
touch db/migrations/${ts}_${name}.down.sql
```

2. **Write up migration** — DDL only (CREATE TABLE, ADD COLUMN, CREATE INDEX, etc.):
 - Always use `IF NOT EXISTS` for CREATE TABLE / CREATE INDEX.
 - Never include DML (INSERT/UPDATE) in migrations — use separate seeder.
 - Add comment at top: `-- Migration: <description>`

3. **Write down migration** — exact inverse:
 - DROP TABLE / DROP COLUMN / DROP INDEX as needed.
 - `IF EXISTS` variants to make down idempotent.

4. **Verify** with the repository-pinned `migrate` binary. Run
 `make tools-bootstrap` first when it is missing:

```sh
migrate -database "$POSTGRES_DSN" -path db/migrations up 1
migrate -database "$POSTGRES_DSN" -path db/migrations down 1
```

## Rules

- One logical change per migration pair.
- Never modify existing migration that has been applied to any environment.
- Column renames: add new column → backfill → drop old (three separate migrations).
- Always test both up and down before committing.
