# Agent guide — cache/twotier/

Unified two-tier read-through cache: **L1** in-process ([cache/memory](../memory),
otter) → **L2** distributed ([cache/redis](../redis), rueidis) → origin loader,
with [singleflight](../singleflight) de-duplication.

A read short-circuits at the first tier that has the key and back-fills the
faster tiers it skipped. A write fans out to both tiers. Concurrent reads of the
same key share one loader call.

## Usage

```go
// Wire (provides *twotier.TwoTier):
fx.New(
    golusoris.Core,
    golusoris.CacheMemory, // *memory.Cache  (L1)
    golusoris.CacheRedis,  // rueidis.Client (L2)
    golusoris.CacheTwoTier,
)

// Get a typed view and read through it:
func NewUserService(tt *twotier.TwoTier) *UserService {
    users := twotier.NewTyped[*User](tt, "user")
    return &UserService{cache: users}
}

func (s *UserService) Load(ctx context.Context, id string) (*User, error) {
    return s.cache.Get(ctx, id, func(ctx context.Context) (*User, error) {
        return s.db.LoadUser(ctx, id) // origin; runs once on a full miss
    })
}
```

## Key API

| Symbol | Purpose |
|---|---|
| `twotier.Module` | fx module — provides `*twotier.TwoTier` |
| `twotier.NewTyped[V](tt, prefix)` | Type-safe view with a key prefix |
| `Typed.Get(ctx, k, loader)` | Read-through L1 → L2 → loader; back-fills tiers |
| `Typed.Set(ctx, k, v)` | Write-through to both tiers |
| `Typed.Delete(ctx, k)` | Removes from both tiers |
| `Typed.InvalidatePrefix(ctx, prefix)` | Bulk-evicts every key under the view + `prefix` from both tiers (`""` clears the whole view) |
| `(*TwoTier).InvalidatePrefix(ctx, prefix)` | Same, but takes an already-composed key prefix (no view prefix added) |

## Prefix invalidation

`InvalidatePrefix` composes the prefix exactly like `Get`/`Set`/`Delete`
(`<view-prefix>:<prefix>`), then evicts from both tiers:

- **L1 (otter)** has no native prefix delete, so it is scanned with `Keys()` and
  matching entries are `Invalidate`d one by one (and forgotten from
  singleflight). O(n) over the live L1 set — fine for the bounded in-process
  cache, not for huge keyspaces.
- **L2 (redis)** is cleared via the `l2` adapter's `DelPrefix`: cursor-paged
  `SCAN MATCH "<prefix>*" COUNT 256` + batched `UNLINK` per page. `UNLINK`
  reclaims memory off the main thread. The adapter refuses an empty composed
  prefix to avoid scanning the whole keyspace.

## Values cross tiers as JSON

L1 stores the live Go value; L2 stores its JSON encoding (Redis is a byte
store, and JSON keeps the cache language-agnostic across replicas). Store
JSON-round-trippable values only.

## Disabled / nil-passthrough mode

A `nil *TwoTier` is a valid no-op cache. A view built from nil (`NewTyped[V](nil,
…)`) calls the loader on every `Get` and makes `Set`/`Delete`/`InvalidatePrefix`
no-ops, so call sites never branch on whether caching is configured.

## Config

```
cache.twotier.l1_ttl = 1m   # L1 TTL (also bounded by cache.memory.ttl)
cache.twotier.l2_ttl = 5m   # L2 TTL, 0 = no expiry
```

## Don't

- Don't use real Redis in tests — the L2 backend sits behind an unexported `l2`
  interface; stub it (see `twotier_test.go`) and use `memory.NewForTest` for L1.
- Don't treat an L2 outage as fatal — `Get` logs and falls through to the
  loader; only `Set`/`Delete` surface L2 errors (the caller chose to write).
- Don't store values that don't JSON-round-trip — L2 holds the JSON, not the
  live object.
- Don't cache by floating-point keys — keys are plain strings; format floats
  canonically before keying.
