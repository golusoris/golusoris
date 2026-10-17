<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/sqlite/

Standalone SQLite atomic store over caller-owned `*sql.DB`.

- `New(db, clk)`: nil database -> error; nil clock -> real clock.
- `EnsureSchema(ctx)` before claims; repeat calls safe.
- Shared algorithm: `../internal/sqlstore/`; dialect statements here.
- Expiry: injected clock, Unix nanoseconds; bounded sweep.
- Verification: file-backed SQLite conformance, no containers.
