<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# sqlc-dev/sqlc — v1.31.1 snapshot

Pinned: **v1.31.1**
Source: [tagged source](https://github.com/sqlc-dev/sqlc/tree/v1.31.1)
Docs: [sqlc documentation](https://docs.sqlc.dev)

## sqlc.yaml (v2 format)

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "queries/"
    schema: "migrations/"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_db_tags: true
        emit_interface: true
        emit_exact_table_names: false
        overrides:
          - db_type: "pg_catalog.timestamptz"
            go_type: "time.Time"
          - db_type: "uuid"
            go_type: { import: "github.com/google/uuid", type: "UUID" }
```

## Query annotations

```sql
-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateUser :one
INSERT INTO users (name, email) VALUES ($1, $2) RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;
```

## Generated code usage

```go
q := db.New(pool)

user, err := q.GetUser(ctx, id)
users, err := q.ListUsers(ctx, db.ListUsersParams{Limit: 20, Offset: 0})
user, err := q.CreateUser(ctx, db.CreateUserParams{Name: "Alice", Email: "alice@example.com"})
err = q.DeleteUser(ctx, id)
```

## With transactions

```go
// golusoris db/sqlc WithTx helper
err = sqlcutil.WithTx(ctx, pool, func(txCtx context.Context, tx pgx.Tx) error {
    queries := db.New(pool).WithTx(tx)
    if _, err := queries.CreateUser(txCtx, params); err != nil {
        return fmt.Errorf("create user: %w", err)
    }
    return nil
})
```

## golusoris usage

- `db/sqlc/` — `WithTx` and `MapError` helpers;
  `tools/sqlc.yaml.fragment` shared configuration.

## Links

- [Tagged source](https://github.com/sqlc-dev/sqlc/tree/v1.31.1)
