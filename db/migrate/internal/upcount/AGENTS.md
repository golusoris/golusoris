<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/migrate/internal/upcount

Test helper shared by the `db/migrate` and `db/migrate/sqlite` module tests.

## Contract

- `Logger(n)` counts the `db/migrate: up complete` record, one per `Migrator.Up` call.
- Keep the message constant equal to the one `Migrator.Up` logs; the module tests fail if they drift.
- Test-only: no production package imports it.
