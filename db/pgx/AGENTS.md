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

## CloudNativePG

| Key | Meaning |
| --- | --- |
| `db.password_file` | role password file (CNPG secret key `password`); overrides DSN password; re-read in `BeforeConnect` -> rotation hits new conns |
| `db.read_dsn` | own `*ReadPool` (CNPG `-ro` service), sessions `default_transaction_read_only=on`; unset -> `*ReadPool` wraps primary |
| `db.ssl.mode` | libpq `sslmode`; overrides DSN |
| `db.ssl.rootcert` / `db.ssl.cert` / `db.ssl.key` | CA + client cert/key files; appended to DSN; re-parsed per new conn (rotation) |

- Env for `password_file` / `read_dsn` needs `config.Options.CompoundKeys` entry (`db.password_file`, `db.read_dsn`), like `connect_timeout`.
- Password read reuses `secrets.File`: 64 KiB cap, regular file only, whitespace trimmed.
- `Options.ConnString(ctx, dsn)` -> DSN with `ssl.*` + `password_file` applied, for connections outside pool (`db/migrate`). URL form: params replaced, password in userinfo. Read once; no rotation.
- TLS reload replaces `TLSConfig` + `Fallbacks` together — no stale `prefer` fallback.
- `*ReadPool` provider lazy: built only when injected. Non-fx: `NewReadPool(ctx, opts, logger, clk)` (needs `ReadDSN`).

## Pinned upstream

- `jackc/pgx/v5` v5.11.0 — see `docs/upstream/pgx/`

## Don't

- Don't use `database/sql` — pgx is only supported driver.
- Don't `time.Now()` inside tracer — it uses `clock.Clock` so tests can fake it.
