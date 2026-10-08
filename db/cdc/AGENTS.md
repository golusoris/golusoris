<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/cdc/

Postgres WAL consumer. Uses `pglogrepl`. Decodes `pgoutput`. Calls one handler.
Feeds `outbox/cdc`.

## API

```go
type Event struct {
    Schema, Table string
    Op            Op                          // INSERT | UPDATE | DELETE | TRUNCATE
    OldValues     map[string]ColumnValue      // canonical typed old values
    NewValues     map[string]ColumnValue      // canonical typed new values
    Old, New      map[string]string           // deprecated lossy text projection
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

```text
cdc.dsn          # replication DSN — REQUIRED, must include replication=database
cdc.slot         # replication slot (default: golusoris)
cdc.publication  # PUBLICATION name (default: golusoris)
cdc.standby_hz   # standby status updates/sec (default: 10; range: 1-1000)
cdc.reconnect_delay # positive retry delay after failed session (default: 1s)
```

## Postgres prerequisites

Need `wal_level = logical`, `pgoutput` slot, and publication. Consumer creates
missing slot. Existing slot is fine.

## Notes

- Empty DSN disables consumer.
- Start LSN is 0. Server resumes slot `confirmed_flush_lsn`.
- Acknowledge only after commit and successful handlers.
- Configured startup requires a non-nil handler.
- Missing relation metadata ends replication without acknowledging commit.
- Handler error ends session. Consumer reconnects. Delivery is at least once.
- Handler must be idempotent.
- Fx startup context does not own long run. Fx stop cancels and joins it.
- Key tuples map only replica-identity columns. Full tuples map relation order.
- Missing tuples remain nil in typed and legacy projections.
- `ColumnValue.Kind` distinguishes text, binary, SQL NULL, and unchanged TOAST.
- Runtime I/O deadlines use real context time. Domain `clock.Clock` schedules retry and standby cadence only.
- Slot names follow Postgres lowercase slot grammar. Publication names are quoted as one identifier.
