<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/sqlc

Runtime helpers for sqlc-generated query packages. shared sqlc.yaml
fragment lives at `tools/sqlc.yaml.fragment` — apps copy/extend it.

## Conventions

- Multi-statement transactions -> `sqlc.WithTx`; never direct `pool.BeginTx`.
- Callback error, commit failure, panic -> bounded rollback with cancellation detached. Original error or panic survives.
- All app code calling sqlc-generated query funcs runs result through `sqlc.MapError` to translate pg constraint codes into golusoris error codes.
- sqlc generation: `sql_package: pgx/v5`, `emit_interface: true`, `emit_pointers_for_null_types: true`. See `tools/sqlc.yaml.fragment`.

## Don't

- Don't bypass `MapError` and check pgconn codes in app code — that's helper's job.
