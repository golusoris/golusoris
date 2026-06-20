# Agent guide — db/cdc/

PostgreSQL logical-replication (WAL) consumer over [jackc/pglogrepl]. Decodes
`pgoutput` messages into structured `Event` values and delivers them to a
caller-supplied `Handler`. Push-based CDC — the building block under
`outbox/cdc`.

## API

```go
type Event struct {
    Schema, Table string
    Op            Op                 // INSERT | UPDATE | DELETE | TRUNCATE
    Old, New      map[string]string  // column → text value
    LSN           pglogrepl.LSN
    CommitTime    time.Time
}
type Handler func(ctx context.Context, ev Event) error

c.SetHandler(h) // must be set before fx Start
```

## Wiring

```go
fx.New(
    cdc.Module,
    fx.Invoke(func(c *cdc.Consumer) { c.SetHandler(myHandler) }),
)
```

- **Provides:** `*Consumer`.
- **Requires:** `*config.Config`, `clock.Clock`, `*slog.Logger`.
- **Config prefix:** `cdc` (env `APP_CDC_*`).

```
cdc.dsn          # replication DSN — REQUIRED, must include replication=database
cdc.slot         # replication slot (default: golusoris)
cdc.publication  # PUBLICATION name (default: golusoris)
cdc.standby_hz   # standby status updates/sec (default: 10)
```

## Postgres prerequisites

`wal_level = logical`; a logical replication slot (`pgoutput`); a `PUBLICATION`
covering the watched tables. The consumer creates the slot if missing
(SQLSTATE 42710 "already exists" is tolerated).

## Notes

- **Empty `cdc.dsn` disables the consumer** (logs and no-ops at start) — safe
  default for apps that don't use CDC.
- Replication starts at LSN 0 to **resume from the slot's
  `confirmed_flush_lsn`**; passing the current WAL head would silently skip
  retained changes (data loss on restart).
- `Old` is only populated for DELETE and for UPDATE under `REPLICA IDENTITY
  FULL`. Unchanged-TOAST columns are omitted from the map. NULLs decode to `""`.
- A handler returning a non-nil error **stops** the consumer.
- Uses `clock.Clock` for standby timing — no `time.Now()`.
