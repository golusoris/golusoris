<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/timescale/

TimescaleDB hypertable + retention helpers for pgx/v5.

TimescaleDB is PostgreSQL extension — same pgxpool from `db/pgx/` is
reused. This package adds thin wrappers around TimescaleDB SQL API.

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

- Don't call `CreateHypertable` on table that already has data in chunks —
 TimescaleDB requires table to be empty or to use `migrate_data => true`.
- Don't use `SetRetention` without `CreateHypertable` first.
- Don't use `Pool()` to bypass timescale helpers for DDL — helpers add
 `if_not_exists => true` to make them startup-safe.
- Policy durations must be positive and microsecond-aligned. Helpers preserve full interval; no hour truncation.
- Compression table names use pgx identifier quoting; pass `table` or `schema.table`, not raw SQL.
