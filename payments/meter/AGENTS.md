# payments/meter

Usage metering for consumption-billed SaaS. Apps record granular
events; the package deduplicates by event ID and aggregates for
billing-period exports.

## Surface

- `meter.NewRecorder(Store, clock.Clock, *slog.Logger)` → `*Recorder`.
- `Recorder.Record(ctx, Event)` — idempotent on `Event.ID`.
- `Recorder.Usage(ctx, customerID, meter, since, until) (float64, error)`.
- `Recorder.List(ctx, Filter) ([]Event, error)` — raw events for audit.
- `meter.Store` interface (Insert, Query, Sum) + `MemoryStore` for tests.

## Idempotency

Every event MUST have a stable `ID`. Recording the same ID twice is a
no-op (logged at debug). Pick IDs that are deterministic at the call
site:

- HTTP requests: the request ID
- Job runs: the job ID + retry attempt
- Webhook deliveries: the webhook event ID

This lets at-least-once delivery (queues, retries) record exactly-once.

## Billing export

Run a periodic job that calls `Recorder.Usage` per customer/meter for
the billing period and posts the totals to your processor's metered-
billing endpoint:

- Stripe Usage Records (`/v1/subscription_items/:id/usage_records`)
- Lemon Squeezy `usage_record`
- Paddle `transaction.adjustment`

The Recorder is processor-agnostic.

## Backends

`MemoryStore` for tests. Production should use a Postgres-backed Store
with a unique index on `event_id` (idempotency) and a partial index on
`(customer_id, meter, at)` for `Usage` performance. Time-series
databases (TimescaleDB hypertables, ClickHouse) work well at high
volume — see `db/timescale` + `db/clickhouse`.
