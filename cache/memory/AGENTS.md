<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — cache/memory/

Typed, in-process L1 cache backed by [otter v2](https://github.com/maypok86/otter).

## Usage

```go
// Wire the module (provides *memory.Cache to the graph):
fx.New(golusoris.Core, memory.Module)

// Get a type-safe view (in an fx.Invoke or constructor):
func NewUserService(c *memory.Cache) *UserService {
    return &UserService{cache: memory.Typed[string, *User](c, "user")}
}
```

## Key API

| Symbol | Purpose |
| --- | --- |
| `memory.Module` | fx module — provides `*memory.Cache` |
| `memory.Typed[K, V](c, prefix)` | Type-safe view with key prefix |
| `TypedCache.Get(k)` | Returns `(V, bool)` — no allocation on miss |
| `TypedCache.Set(k, v)` | Returns `bool`: true on insert, false on replacement |
| `TypedCache.Delete(k)` | Invalidates the key |
| `TypedCache.SetExpiresAfter(k, ttl)` | Per-entry expiry override |

## Config

```ini
cache.memory.max_size = 10000   # max entries (default 10_000)
cache.memory.ttl      = 5m      # write TTL, 0 = no expiry (default 5m)
```

Zero default TTL still permits positive per-entry `SetExpiresAfter` overrides.
Nonpositive maximum size and negative TTL fail construction.

## Don't

- Don't store pointer-heavy structs that outlive their owner — otter holds references, not copies.
- Don't use as distributed cache — it's per-replica. Use `cache/redis` for cross-replica sharing.
- Typed key identity uses Go equality plus namespace; no string formatting.
