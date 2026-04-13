# ADR-0005: river over asynq for background jobs

- **Status**: Accepted
- **Date**: 2026-04-13 (backfill)
- **Deciders**: @lusoris
- **Tags**: jobs, db

## Context

Apps need durable background jobs (emails, exports, webhooks). The two contenders for Postgres-backed Go job queues:

- **[riverqueue/river](https://github.com/riverqueue/river)** — Postgres-only, uses LISTEN/NOTIFY + `SELECT ... FOR UPDATE SKIP LOCKED`, typed args via generics.
- **[hibiken/asynq](https://github.com/hibiken/asynq)** — Redis-only, mature web UI, scheduled tasks.

Apps already use Postgres (per [ADR-0001](0001-fx-over-wire-for-di.md) wiring + `db/pgx`); adding Redis just for jobs doubles operational surface. The framework also wants the **transactional outbox** pattern — write job alongside domain rows in the same `pgx.Tx` so partial failures are impossible. That requires the job queue to live in the same DB.

## Decision

We will use `github.com/riverqueue/river` as the background job system. The framework provides `jobs/` (river client + workers registry as fx modules) and `outbox/` (transactional outbox → river dispatcher).

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| `hibiken/asynq` | Mature, web UI, multi-queue priorities | Redis adds an operational dep; loses transactional consistency with Postgres rows | Doubles infrastructure for marginal gain. |
| `gocraft/work` | Simple, Redis-based | Unmaintained since 2021 | Dead. |
| Hand-rolled `pgq` (LISTEN/NOTIFY + a table) | Total control | River already does this correctly with retries, dead letters, periodic jobs | Reinventing river badly. |
| `temporal.io` | Workflow engine, not just jobs | Server install, learning curve, overkill for "send email" | Right tool for orchestration, wrong tool for fire-and-forget. |

## Consequences

- **Positive**: One database to operate. `outbox.Add(ctx, tx, ...)` in the same transaction as domain writes guarantees exactly-once enqueue. Typed args via generics catch arg/worker mismatches at compile time. River's web UI (`jobs/ui/`) opt-in.
- **Negative**: Postgres-bound — apps that move jobs off Postgres (e.g. to SQS) must also rewrite the outbox dispatcher. Job throughput capped by Postgres write rate (acceptable for our target workloads — saturated at ~10k jobs/s on commodity hardware per river benchmarks).
- **Follow-ups**: `outbox/` shipped as a separate module (per [ADR-0006](0006-pluggable-leader-election.md)) so only the leader replica drains. Periodic jobs handled by `jobs/cron/` wrapping river's `PeriodicJob`.

## References

- river pinned at v0.34.0 — see [`docs/upstream/river/`](../upstream/river/).
- [`outbox/AGENTS.md`](../../outbox/AGENTS.md) — dispatcher contract.
- [`jobs/AGENTS.md`](../../jobs/AGENTS.md) — worker registration patterns.
