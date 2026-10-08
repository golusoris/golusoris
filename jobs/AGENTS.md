<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — jobs/

Background job queue backed by Postgres via [river].

## Conventions

- One `*jobs.Workers` per app; register workers before fx graph starts. Return
 `jobs.Register(w, &MyWorker{})` from Fx invoke — avoids direct river import and
 propagates invalid or duplicate registration.
- Insert-only clients: set `jobs.producer_only=true`. Module builds client
 without queues and skips worker lifecycle; `Insert(ctx, args, nil)` remains
 available. Completion observation is no-op: no local worker events
 exist.
- One `default` queue is pre-configured. Apps needing additional named
 queues extend via `fx.Decorate(...)` — keeping baseline small.
- Retry + timeout defaults (25 attempts, 30s per job) match river's
 production conventions. Per-worker overrides go in Worker impl.
- `jobs.retry.base > 0` swaps River's attempt^4 backoff for `RetryPolicy`:
 base * 2^(n-1), capped by `jobs.retry.max`, +/- `jobs.retry.jitter`. "Now"
 comes from `clock.Clock` (fx graph or `Options.Clock`).
- fx Stop runs `Drain`: soft `Stop` for `jobs.stop.soft` (10s), then
 `StopAndCancel` for `jobs.stop.hard` (5s), both bounded by fx stop ctx.
 Workers must honour ctx cancellation or hard phase returns error.
- Define + insert jobs via aliases (`jobs.JobArgs`, `jobs.Job[T]`,
 `jobs.WorkerDefaults[T]`, `jobs.InsertOpts`, `jobs.UniqueOpts`,
 `jobs.JobCancel`) — no river import in app code.
- `NewClient[TTx]` + `AppendLifecycle[TTx]` + `ObserveClient[TTx]` are
 driver-generic building blocks for driver packages (jobs/sqlite).
- `jobs.MetricsModule` -> `*DepthCollector` on Prometheus: `river_queue_available`,
 `river_queue_oldest_available_age_seconds`, `river_jobs{queue,state[,tenant]}`,
 `river_depth_collector_up`. One grouped query per `jobs.metrics.cache_ttl`
 (10s), bounded by `jobs.metrics.query_timeout` (2s) + `MaxDepthRows`.
 Tenant label from `metadata[jobs.metrics.tenant_key]`; top `tenant_top_n`
 (20) kept, rest `other`. Queues: configured always kept, rest top-N to
 `max_queues` (64). Querier: graph `jobs.DepthQuerier` else Postgres pool.
- `DepthCollector.QueueDepth` = available + running; unknown queue ->
 `ErrUnknownQueue`. List scaled queues in `jobs.metrics.queues` so empty
 queues stay known (scale-to-zero).
- `jobs.tracing.enabled` adds otelriver (MPL-2.0, unmodified import) spans +
 `river.*` metrics on global OTel providers; `jobs.tracing.propagate` puts
 `traceparent` in job metadata.

## Subpackages

| Subpackage | Purpose |
| --- | --- |
| `jobs/cron` | robfig/cron/v3 parser + `Register[T](client, expr, ctor)` helper |
| `jobs/ui` | River UI at configurable prefix with optional basic-auth |
| `jobs/sqlite` | River on SQLite (riversqlite) for standalone single binary |

## Don't

- Don't import `github.com/riverqueue/river` directly from app code —
 use `jobs.Register`, `jobs.Client`, `jobs.Workers`. Keeps river
 major-version rip-and-replace contained to this package.
- Don't Start river client manually — fx does it. Stop-on-context
 ensures in-flight jobs drain before SIGTERM's grace window runs out.
