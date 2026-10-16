<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/pgxpoolstate

Zero `pgxpool.Pool` detection without nil dereference.

## Contract

- `Initialized(pool)`: false for nil, zero `pgxpool.Pool`, or unknown pgxpool layout.
- Reads unexported `config` field via `reflect`; no method call. pgxpool
  `Config()`, `Stat()`, `Reset()` dereference nil on zero pool.
- Never detect zero pool via method call under `recover()`. Recovered nil
  dereference corrupts heap on Windows hosts with large exception contexts
  (golang/go#81238). Gate: `make fault-recovery`.
- pgxpool bump: `TestInitializedAcceptsConstructedPool` green required; red
  means field renamed, callers then reject every pool.
