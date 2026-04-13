# Agent guide — audit/

Append-only structured audit log. Records who did what to which resource with
optional before/after `Diff` and arbitrary `Metadata`.

## Core types

| Type | Purpose |
|---|---|
| `Event` | Immutable audit record: Actor, Action, Target, Diff, Metadata, CreatedAt |
| `Diff` | `map[string]FieldChange` — before/after per field |
| `Filter` | Restricts `List` by Actor, Action, Target, TenantID, time bounds, Limit |
| `Store` | Persistence interface — implement with Postgres; `MemoryStore` for tests |
| `Logger` | Wraps `Store`; auto-assigns ID + CreatedAt via injected `clock.Clock` |

## Usage

```go
logger := audit.New(store, audit.WithClock(clk))
_ = logger.Log(ctx, audit.Event{
    Actor:  "user:42",
    Action: "order.cancel",
    Target: "order:99",
    Diff:   audit.Diff{"status": {Before: "pending", After: "cancelled"}},
})
```

## Don't

- Don't mutate stored events — the store is append-only by contract.
- Don't put PII in Metadata without confirming GDPR erasure strategy.
- Don't call `Log` on the hot read path — batch or async-queue high-volume events.
