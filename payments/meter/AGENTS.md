<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# payments/meter

Usage metering for consumption-billed SaaS. Apps record granular
events; package deduplicates by event ID and aggregates for
billing-period exports.

## Surface

- `meter.NewRecorder(Store, clock.Clock, *slog.Logger)` → `*Recorder`.
- `Recorder.Record(ctx, Event)` — idempotent on `Event.ID`.
- `Recorder.Usage(ctx, customerID, meter, since, until) (float64, error)`.
- `Recorder.List(ctx, Filter) ([]Event, error)` — raw events for audit.
- `meter.Store` interface (Insert, Query, Sum) + `MemoryStore` for tests.

## Idempotency

Every event MUST have stable `ID`. Recording same ID twice is no-op (logged at debug). Pick IDs that are deterministic at call
site:

- HTTP requests: request ID
- Job runs: job ID + retry attempt
- Webhook deliveries: webhook event ID

This lets at-least-once delivery (queues, retries) record exactly-once.

## Billing export

Run periodic job that calls `Recorder.Usage` per customer/meter for
billing period and posts totals to your processor's metered-
billing endpoint:

- Stripe Usage Records (`/v1/subscription_items/:id/usage_records`)
- Lemon Squeezy `usage_record`
- Paddle `transaction.adjustment`

Recorder is processor-agnostic.

## Backends

`MemoryStore` for tests. Production should use Postgres-backed Store
with unique index on `event_id` (idempotency) and partial index on
`(customer_id, meter, at)` for `Usage` performance. Time-series
databases (TimescaleDB hypertables, ClickHouse) work well at high
volume — see `db/timescale` + `db/clickhouse`.
