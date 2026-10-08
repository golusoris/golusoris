<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — outbox/

Transactional outbox: write events in same pg tx as domain changes;
drainer atomically enqueues River jobs + marks rows dispatched.

## Conventions

- Apps call `outbox.Add(ctx, tx, kind, payload)` inside their existing
 transaction (alongside `sqlc.WithTx` or any pgx tx). If tx
 rolls back, event rolls back too.
- `kind` discriminates events. Apps usually mirror River `JobArgs.Kind()`.
- `payload` is JSON-marshalable. `json.RawMessage` and `[]byte` pass
 through verbatim.
- Multiple replicas may run `outbox.Module`. `FOR UPDATE SKIP LOCKED`
 partitions batches; River insert + dispatch marker share one pg transaction.
- Each drain has a 30-second default deadline. Rollback uses its own
 five-second cleanup deadline after drain or shutdown cancellation.
- Dispatchers must honor their context and return before its deadline.
- Pending defaults to 100 rows, caps at 1000, rejects negative limits.
- Failed rows gain an attempt and one-second default backoff. Due time plus ID
 ordering lets retries re-enter ahead of later fresh traffic without hot loops.
- Dispatch markers require exactly one row. Failed markers require non-nil cause.
- River inserts use savepoints. SQL rejection rolls back before failure
 metadata is recorded in outer transaction.
- Handoff creates one River job per outbox row. River job execution remains
 at-least-once; workers must be idempotent.

## CloudEvents envelope

```go
err := outbox.AddEvent(ctx, tx, "job.completed", job, outbox.Envelope{
    Source: "/vmafx/controller", Subject: "job/42", Tenant: "acme",
    DataSchema: "https://schemas.example.com/job.json",
})
ce, err := ev.CloudEvent("/vmafx/controller") // default source for rows without one
```

- `Add` = `AddEvent` with empty `Envelope`.
- `event_id`: UUID, assigned once at insert (`gen_random_uuid()`) or from
 `Envelope.EventID` (canonicalised; duplicate -> unique violation). Same id on
 every retry/replay -> CloudEvents `id` + NATS `Nats-Msg-Id`.
- Trace: empty `TraceParent` -> W3C trace context of ctx span
 (`propagation.TraceContext`). Travels as `traceparent`/`tracestate`
 extensions. `Tenant` -> `tenant` extension (`outbox.TenantExtension`).
- Mapping: kind -> `type`, created_at -> `time`, payload -> data
 (`application/json`).
- Invalid envelope -> `ErrInvalidEnvelope` before insert; attribute named by
 `*cloudevents.AttributeError` in chain.
- Publishing: CDC sinks `NewNATSCloudEventSink` / `NewKafkaCloudEventSink`
 (`outbox/cdc`). Polling drainer hands `Event` to River dispatcher; worker
 may publish `ev.CloudEvent(...)` via `nats.Client.PublishCloudEvent`.

## Migration

`outbox/migrations/` ships schema as golang-migrate pairs. Wire
via `dbmigrate.Options{}.WithFS(outbox.MigrationsFS)` or copy SQL
into app's own migrations directory.

- `20261007000003_outbox_cloudevents` adds `event_id`, `source`, `subject`,
 `data_schema`, `tenant`, `traceparent`, `tracestate` + unique index on
 `event_id`. Apply before deploying code that calls `Add`/`Pending`.
 Volatile default rewrites existing rows under exclusive lock.

## Dispatcher contract

```go
func dispatcher(
    ctx context.Context,
    ev outbox.Event,
) (river.JobArgs, *river.InsertOpts, error) {
    switch ev.Kind {
    case "order.created":
        var a OrderCreatedArgs
        if err := outbox.Unmarshal(ev, &a); err != nil { return nil, nil, err }
        return a, nil, nil
    default:
        return nil, nil, fmt.Errorf("unknown kind %q", ev.Kind)
    }
}
```

Returning `(nil, nil, nil)` drops event (marks dispatched without
enqueuing). Useful for events whose downstream no longer cares.

## Don't

- Don't call `outbox.Add` outside transaction. whole point is
 atomic write — non-tx insert is unreliable river
 insert with extra steps.
- Don't enqueue outside drainer transaction. Separate insert + marker commits
 reopen crash window and duplicate jobs.
- Don't store huge blobs in payload. JSONB has its limits + you'll
 blow up river job too. Reference external store.
