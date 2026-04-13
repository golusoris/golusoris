# Agent guide — testutil/pg

Boots a real Postgres container via testcontainers-go for tests that need a
genuine database. Docker is a hard requirement (no fake/mock fallback).

## Conventions

- `pg.Start(t)` returns a connected `*pgxpool.Pool`. The container + pool are torn down via `t.Cleanup`. Each call gets its own container — tests are isolated.
- For tests that need only the DSN (e.g. driving `db/migrate`), use `pg.DSN(t)`.
- Default image is `postgres:17-alpine`. Override via `Options.Image`.

## Don't

- Don't share containers across test files via package-level vars — use TestMain or a sync.Once helper if you genuinely need to amortize startup cost.
- Don't `t.Skip()` when Docker is missing — a CI without Docker is a CI bug.
