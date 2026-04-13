# Agent guide — outbox/

Transactional outbox: write events in the same pg tx as domain changes,
then a leader-gated drainer enqueues them as river jobs.

## Conventions

- Apps call `outbox.Add(ctx, tx, kind, payload)` inside their existing
  transaction (alongside `sqlc.WithTx` or any pgx tx). If the tx
  rolls back, the event is rolled back too — exactly-once semantics
  at the write side.
- `kind` is the discriminator on the event. Apps usually mirror the
  river `JobArgs.Kind()` to make the dispatcher trivial.
- `payload` is JSON-marshalable. `json.RawMessage` and `[]byte` pass
  through verbatim.
- Run the `outbox.Module` under a leader (`leader/k8s` or `leader/pg`)
  so only one replica drains. Two drainers won't double-dispatch
  (river-side de-dup catches that), but they'll waste cycles.

## Migration

`outbox/migrations/` ships the schema as a golang-migrate pair. Wire
via `dbmigrate.Options{}.WithFS(outbox.MigrationsFS)` or copy the SQL
into the app's own migrations directory.

## Dispatcher contract

```go
func dispatcher(ctx context.Context, ev outbox.Event) (river.JobArgs, *river.InsertOpts, error) {
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

Returning `(nil, nil, nil)` drops the event (marks dispatched without
enqueuing). Useful for events whose downstream no longer cares.

## Don't

- Don't call `outbox.Add` outside a transaction. The whole point is
  the atomic write — a non-tx insert is just an unreliable river
  insert with extra steps.
- Don't run the drainer on every replica. Wrap with `leader/`. A
  drainer running concurrently can OK-but-double the work.
- Don't store huge blobs in payload. JSONB has its limits + you'll
  blow up the river job too. Reference an external store.
