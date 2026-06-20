# Agent guide — outbox/cdc/

A **CDC-based** drain for the transactional outbox. Instead of polling the outbox
table (as `outbox.Drainer` does), it subscribes to the PostgreSQL WAL via
`db/cdc.Consumer` and forwards each committed outbox row to a `Sink` — lower
latency, no poll interval, no `SELECT ... FOR UPDATE` contention.

## API

```go
fx.New(
    golusoris.Core, golusoris.DB,
    cdc.Module,                       // provides *cdc.Drainer
    fx.Supply(cdc.NewKafkaSink(kc, "events")), // or NewNATSSink(...)
)
```

- **Provides**: `*cdc.Drainer` (runs under fx.Lifecycle; must run under a leader).
- **Requires**: `db/cdc.Consumer` (logical replication) + a `Sink`.
- **Config**: `DefaultConfig()`; prefix `outbox.cdc`.

```go
type Sink interface { Send(ctx, []Message) error }   // KafkaSink, NATSSink provided
```

## Notes

- Run under `leader/` so exactly one replica consumes the WAL slot.
- Complements (does not replace) the poll-based `outbox.Drainer` — pick one per app.
- Requires a Postgres logical-replication slot; see `db/cdc/AGENTS.md`.
