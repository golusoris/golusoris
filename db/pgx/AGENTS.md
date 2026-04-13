# Agent guide — db/pgx

Provides `*pgxpool.Pool` as an fx dependency. Reads config from `config.Config["db"]`.

## Conventions

- All app code that needs a Postgres connection injects `*pgxpool.Pool` from this module — never call `pgxpool.New*` directly.
- Slow-query logging is on by default at 200ms. Set `db.tracing.slow=0` to disable.
- Connection retry on start uses exponential backoff (defaults: 10 attempts, 50ms→5s). Tune via `db.retry.*` keys.

## Pinned upstream

- `jackc/pgx/v5` v5.9.1 — see `docs/upstream/pgx/` (planned)

## Don't

- Don't use `database/sql` — pgx is the only supported driver.
- Don't `time.Now()` inside the tracer — it uses `clock.Clock` so tests can fake it.
