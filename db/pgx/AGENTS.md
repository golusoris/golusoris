<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/pgx

Provides `*pgxpool.Pool` as fx dependency. Reads config from `config.Config["db"]`.

## Conventions

- All app code that needs Postgres connection injects `*pgxpool.Pool` from this module — never call `pgxpool.New*` directly.
- Slow-query logging is on by default at 200ms. Set `db.tracing.slow=0` to disable.
- Connection retry on start uses exponential backoff (defaults: 10 attempts, 50ms→5s). Tune via `db.retry.*` keys.
- Pool bounds: min nonnegative; max positive; min <= max. Durations nonnegative.
- Retry attempts and delays positive; initial <= max; doubling saturates at max.
- Nil and typed-nil custom tracers ignored before multitracer composition.

## Readiness

- Opt-in `pgx.ReadinessModule` -> registers `ReadinessCheck(pool, timeout, logger)` (name `postgres`, Ping within 1s) on app `*statuspage.Registry`. Exhausted pool -> Ping waits on acquire -> readiness fails.

## Pinned upstream

- `jackc/pgx/v5` v5.11.0 — see `docs/upstream/pgx/`

## Don't

- Don't use `database/sql` — pgx is only supported driver.
- Don't `time.Now()` inside tracer — it uses `clock.Clock` so tests can fake it.
