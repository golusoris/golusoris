# Agent guide — db/timescale/

TimescaleDB hypertable + retention helpers for pgx/v5.

TimescaleDB is a PostgreSQL extension — the same pgxpool from `db/pgx/` is
reused. This package adds thin wrappers around the TimescaleDB SQL API.

## Usage

```go
pool, _ := pgxpool.New(ctx, dsn) // TimescaleDB-enabled Postgres
ts := timescale.New(pool)

// Convert existing table to hypertable (idempotent):
_ = ts.CreateHypertable(ctx, "metrics", "time")

// Drop data older than 30 days automatically:
_ = ts.SetRetention(ctx, "metrics", 30*24*time.Hour)

// Enable columnar compression + policy:
_ = ts.EnableCompression(ctx, "metrics")
_ = ts.AddCompressionPolicy(ctx, "metrics", 7*24*time.Hour)

// Raw pgx pool for normal queries + time_bucket aggregations:
pool.QueryRow(ctx,
    "SELECT time_bucket('1 hour', time) AS bucket, avg(value) FROM metrics GROUP BY 1")
```

## Don't

- Don't call `CreateHypertable` on a table that already has data in chunks —
  TimescaleDB requires the table to be empty or to use `migrate_data => true`.
- Don't use `SetRetention` without `CreateHypertable` first.
- Don't use `Pool()` to bypass timescale helpers for DDL — the helpers add
  `if_not_exists => true` to make them startup-safe.
