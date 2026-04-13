# Agent guide — db/sqlc

Runtime helpers for sqlc-generated query packages. The shared sqlc.yaml
fragment lives at `tools/sqlc.yaml.fragment` — apps copy/extend it.

## Conventions

- All app code wraps multi-statement transactions through `sqlc.WithTx` — never `pool.BeginTx` directly. This guarantees rollback on every error path.
- All app code calling sqlc-generated query funcs runs the result through `sqlc.MapError` to translate pg constraint codes into golusoris error codes.
- sqlc generation: `sql_package: pgx/v5`, `emit_interface: true`, `emit_pointers_for_null_types: true`. See `tools/sqlc.yaml.fragment`.

## Don't

- Don't bypass `MapError` and check pgconn codes in app code — that's the helper's job.
