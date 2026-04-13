# Agent guide — db/clickhouse/

fx-wired ClickHouse OLAP client via ClickHouse/clickhouse-go/v2.

## fx wiring

```go
fx.New(clickhouse.Module) // reads "db.clickhouse.*" from koanf config
```

Config keys (prefix `db.clickhouse`):

| Key | Default | Purpose |
|---|---|---|
| `addr` | `["localhost:9000"]` | ClickHouse server(s) |
| `database` | `"default"` | Target database |
| `username` | `"default"` | Auth username |
| `password` | `""` | Auth password |
| `tls` | `false` | Enable TLS |

## Usage

```go
// DDL / DML:
err := db.Exec(ctx, "CREATE TABLE IF NOT EXISTS events (...) ENGINE=MergeTree()")

// Query:
rows, err := db.Query(ctx, "SELECT count() FROM events WHERE ts > now() - INTERVAL 1 HOUR")
defer rows.Close()
for rows.Next() {
    var count uint64
    _ = rows.Scan(&count)
}

// Advanced (batch inserts, transactions):
conn := db.Conn() // underlying driver.Conn
batch, _ := conn.PrepareBatch(ctx, "INSERT INTO events")
_ = batch.Append(...)
_ = batch.Send()
```

## Don't

- Don't use this for transactional workloads — ClickHouse is append-optimised OLAP.
- Don't issue frequent small INSERTs — batch with `PrepareBatch` or use an async insert buffer.
