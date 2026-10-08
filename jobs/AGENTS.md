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

## Subpackages

| Subpackage | Purpose |
| --- | --- |
| `jobs/cron` | robfig/cron/v3 parser + `Register[T](client, expr, ctor)` helper |
| `jobs/ui` | River UI at configurable prefix with optional basic-auth |

## Don't

- Don't import `github.com/riverqueue/river` directly from app code —
 use `jobs.Register`, `jobs.Client`, `jobs.Workers`. Keeps river
 major-version rip-and-replace contained to this package.
- Don't Start river client manually — fx does it. Stop-on-context
 ensures in-flight jobs drain before SIGTERM's grace window runs out.
