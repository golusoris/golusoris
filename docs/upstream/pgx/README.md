# jackc/pgx/v5 — v5.9.1 snapshot

Pinned: **v5.9.1**
Source: https://pkg.go.dev/github.com/jackc/pgx/v5@v5.9.1

## Key API surface

### Pool

```go
pool, err := pgxpool.New(ctx, connString)
pool, err := pgxpool.NewWithConfig(ctx, config)
conn, err := pool.Acquire(ctx)          // returns *pgxpool.Conn
defer conn.Release()
pool.Close()

// Config
cfg, _ := pgxpool.ParseConfig(dsn)
cfg.MaxConns = 10
cfg.MinConns = 2
cfg.MaxConnLifetime = time.Hour
cfg.MaxConnIdleTime = 30 * time.Minute
cfg.ConnConfig.Tracer = &myTracer{}     // QueryTracer interface
```

### Querying

```go
rows, err := pool.Query(ctx, sql, args...)
row  := pool.QueryRow(ctx, sql, args...)
tag, err := pool.Exec(ctx, sql, args...)

// pgx.CollectRows / pgx.ForEachRow
items, err := pgx.CollectRows(rows, pgx.RowToStructByName[MyStruct])
```

### Transactions

```go
tx, err := pool.Begin(ctx)
tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
err = tx.Commit(ctx)
err = tx.Rollback(ctx)
```

### Named arguments / pgtype

```go
// pgtype.Text, pgtype.Int4, pgtype.Timestamptz, etc.
var t pgtype.Timestamptz
t.Scan(someTime)

// pgx.NamedArgs (map-style)
pool.Exec(ctx, "UPDATE foo SET bar=@bar WHERE id=@id",
    pgx.NamedArgs{"bar": val, "id": id})
```

### Errors

```go
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) {
    pgErr.Code   // SQLSTATE, e.g. "23505" = unique violation
    pgErr.Detail
}
```

Common SQLSTATE codes:
- `23505` — unique_violation
- `23503` — foreign_key_violation
- `42710` — duplicate_object (e.g. replication slot already exists)

### Tracer interface

```go
type QueryTracer interface {
    TraceQueryStart(ctx context.Context, conn *pgx.Conn, data TraceQueryStartData) context.Context
    TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data TraceQueryEndData)
}
```

## golusoris usage

- `db/pgx/` — `*pgxpool.Pool` provided as fx singleton; koanf-driven config.
- `db/sqlc/` — `WithTx(pool, fn)` helper + `MapError`.
- `testutil/pg/` — testcontainers postgres, returns pool.

## Links

- Changelog: https://github.com/jackc/pgx/blob/master/CHANGELOG.md
- Wiki: https://github.com/jackc/pgx/wiki
