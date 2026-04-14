# auth/lockout

Per-identity login lockout: counts failed attempts inside a window and locks the identity for a cooldown.

## Surface

- `lockout.New(store, clk, opts)` — service constructor; `clk` may be nil.
- `Check(ctx, key)` — returns `gerr.Unauthorized` while locked.
- `Fail(ctx, key)` / `Reset(ctx, key)` — record / clear attempts.
- `MemoryStore` — in-process store for tests.

## Notes

- Counter resets when a fail arrives outside `Window`.
- Lock duration = `Cooldown`. After it elapses, `Check` returns nil.
- Backing store is pluggable (Redis, Postgres, etc.) via `Store`.
