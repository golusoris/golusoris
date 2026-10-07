<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/lockout

Per-identity login lockout: counts failed attempts inside window and locks identity for cooldown.

## Surface

- `lockout.New(store, clk, opts)` → `(*Service, error)`; `clk` may be nil.
- `Check(ctx, key)` — returns `gerr.Unauthorized` while locked.
- `Fail(ctx, key)` / `Reset(ctx, key)` — record / clear attempts.
- `Store.RecordFailure` — atomic window reset + increment + lock transition.
- `MemoryStore` — in-process store for tests.

## Notes

- Counter resets when fail arrives outside `Window`.
- Zero policy fields use defaults; negative values are rejected.
- `Check`, `Fail`, and `Reset` reject blank identity keys.
- Concurrent failures never lose increments; adapters implement one atomic operation.
- Lock duration = `Cooldown`. After it elapses, `Check` returns nil.
- Backing store is pluggable (Redis, Postgres, etc.) via `Store`.
