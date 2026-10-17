<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/redis/

Redis / Valkey atomic store over caller-owned `rueidis.Client`.

- `New(client, prefix)`: nil / typed-nil client -> error.
- Empty prefix -> `DefaultRedisPrefix`.
- Single-key Lua scripts; Redis Cluster compatible. Server PX expiry; no sweeper.
- TTL rounded up to milliseconds; positive submillisecond TTL -> 1 ms.
- Integration tests: `testutil/redis` container; judge rerun when unavailable.
