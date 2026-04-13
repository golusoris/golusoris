# Agent guide — jobs/

Background job queue backed by Postgres via [river].

## Conventions

- One `*jobs.Workers` per app; register workers before the fx graph
  starts. Use `jobs.Register(w, &MyWorker{})` — avoids direct river
  import.
- Insert-only clients: apps that only produce jobs (no queues
  configured) still get a client for `Insert(ctx, args, nil)`. Workers
  nil → no queues registered → client doesn't Start.
- One `default` queue is pre-configured. Apps needing additional named
  queues extend via `fx.Decorate(...)` — keeping the baseline small.
- Retry + timeout defaults (25 attempts, 30s per job) match river's
  production conventions. Per-worker overrides go in the Worker impl.

## Subpackages

| Subpackage | Purpose |
|---|---|
| `jobs/cron` | robfig/cron/v3 parser + `Register[T](client, expr, ctor)` helper |
| `jobs/ui`   | River UI at a configurable prefix with optional basic-auth |

## Don't

- Don't import `github.com/riverqueue/river` directly from app code —
  use `jobs.Register`, `jobs.Client`, `jobs.Workers`. Keeps the river
  major-version rip-and-replace contained to this package.
- Don't Start the river client manually — fx does it. Stop-on-context
  ensures in-flight jobs drain before SIGTERM's grace window runs out.
