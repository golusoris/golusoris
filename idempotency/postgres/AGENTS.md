<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/postgres/

PostgreSQL atomic store over caller-owned `*pgxpool.Pool`.

- `New(pool, clk)`: nil pool -> error; nil clock -> real clock.
- Apply `MigrationsFS`, path `migrations`, before claims.
- Shared algorithm: `../internal/sqlstore/`; dialect statements here.
- Expiry: injected clock; sweep uses bounded `SKIP LOCKED` deletion.
- Integration tests: `testutil/pg` container; judge rerun when unavailable.
