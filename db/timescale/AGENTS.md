<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/timescale/

TimescaleDB helpers for pgx/v5: hypertables, chunk interval, compression,
retention, continuous aggregates. Edition-aware: detect first, fail typed.

TimescaleDB is PostgreSQL extension — same pgxpool from `db/pgx/` is
reused.

## Editions

| Edition | `timescaledb.license` | Features |
| --- | --- | --- |
| `EditionNone` | any; no `pg_extension` row | none -> `ErrExtensionMissing`; tables stay plain |
| `EditionApache` | `apache` (`-oss` image) | `CreateHypertable*`, `DropChunks` |
| `EditionCommunity` | `timescale` (TSL) | all |
| `EditionUnknown` | other value | Apache features only; TSL features fail closed |

- `Capabilities(ctx)` = one query: `pg_extension.extversion` + `current_setting('timescaledb.license', true)`.
- Licence fixed per server start (`SET` refused at runtime), so per-call detection is cheap + never stale.
- Every helper calls `require(feature)` before DDL -> `*UnsupportedError`;
 `errors.Is(err, ErrUnsupportedEdition)` or `errors.Is(err, ErrExtensionMissing)`.
- Retention fallback under Apache: schedule `DropChunks(ctx, table, olderThan)` yourself (River periodic job).

## Usage

```go
ts := timescale.New(pool)
_ = ts.CreateHypertableWithOptions(ctx, "metrics", "time",
    timescale.HypertableOptions{ChunkTimeInterval: 6 * time.Hour})
_ = ts.EnableCompressionWithOptions(ctx, "metrics", timescale.CompressionOptions{
    SegmentBy: []string{"device"},
    OrderBy:   []timescale.OrderColumn{{Column: "time", Descending: true}},
})
_ = ts.AddCompressionPolicy(ctx, "metrics", 7*24*time.Hour)
_ = ts.CreateContinuousAggregate(ctx, timescale.ContinuousAggregate{
    Name:  "metrics_hourly",
    Query: `SELECT time_bucket('1 hour', time) AS bucket, device, avg(val) FROM metrics GROUP BY 1, 2`,
})
_ = ts.AddContinuousAggregatePolicy(ctx, "metrics_hourly", timescale.RefreshPolicy{
    StartOffset: 7 * 24 * time.Hour, EndOffset: time.Hour, ScheduleInterval: time.Hour,
})
if err := ts.SetRetention(ctx, "metrics", 30*24*time.Hour); errors.Is(err, timescale.ErrUnsupportedEdition) {
    _, _ = ts.DropChunks(ctx, "metrics", 30*24*time.Hour) // Apache fallback
}
```

## fx

`timescale.Module` provides `*DB` over `*pgxpool.Pool`; OnStart probes edition (bounded by `detect_timeout`), logs it, enforces `require`.

| Key | Default | Meaning |
| --- | --- | --- |
| `db.timescale.require` | `""` | `""`/`none` no check; `apache` extension required; `community` TSL required |
| `db.timescale.detect_timeout` | `5s` | start probe bound |

## Tests

- Internal fake querier: unknown licence fails closed, missing extension, detect error, SQL builders, boundaries.
- Integration: `TestCommunityEdition` (`testimages.Timescale`), `TestApacheEdition` (`testimages.TimescaleApache`), `TestWithoutExtension` (plain Postgres).

## Don't

- Don't call `CreateHypertable` on table that already has data in chunks —
 TimescaleDB requires table to be empty or to use `migrate_data => true`.
- Don't use `SetRetention` without `CreateHypertable` first.
- Don't use `Pool()` to bypass helpers for DDL — helpers add edition checks + `if_not_exists => true`.
- Don't build `ContinuousAggregate.Query` from user input — embedded verbatim; semicolons rejected.
- Policy durations must be positive and microsecond-aligned. Zero refresh offsets mean SQL `NULL` (unbounded).
- Table/column names use pgx identifier quoting; pass `table` or `schema.table`, not raw SQL.
- `RefreshContinuousAggregate` windows are `timestamptz`; integer-time hypertables need raw SQL.
