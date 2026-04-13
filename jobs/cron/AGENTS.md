# Agent guide — jobs/cron

robfig/cron/v3 parser + helper for registering periodic river jobs.

## Conventions

- Grammar: 5-field classic cron (`minute hour day month weekday`) +
  descriptors (`@hourly`, `@daily`, `@every 30s`, …). Sub-minute
  cadence via `@every Ns`.
- `cron.Validate(expr)` for config-load validation — fail fast, not at
  runtime.
- `cron.Register(client, expr, ctor)` adds a river PeriodicJob. The
  constructor returns the JobArgs that will be inserted on each tick.

## Don't

- Don't schedule work below `@every 5s` — river's periodic scheduler
  has a polling overhead. Use a real worker + ticker for high-frequency
  background work.
