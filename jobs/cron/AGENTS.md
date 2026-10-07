<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — jobs/cron

robfig/cron/v3 parser + helper for registering periodic river jobs.

## Conventions

- Grammar: 5-field classic cron (`minute hour day month weekday`) +
 descriptors (`@hourly`, `@daily`, `@every 30s`, …). Sub-minute
 cadence via `@every Ns`.
- `cron.Validate(expr)` for config-load validation — fail fast, not at
 runtime.
- `cron.Register(client, expr, ctor)` adds river PeriodicJob. Constructor
 returns JobArgs inserted on each tick. Nil clients and constructors return
 errors. Producer-only clients return an error because periodic scheduling
 requires configured queues and workers.

## Don't

- Don't schedule work below `@every 5s` — river's periodic scheduler
 has polling overhead. Use real worker + ticker for high-frequency
 background work.
