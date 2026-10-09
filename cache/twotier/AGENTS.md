<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — cache/twotier/

Unified two-tier read-through cache: **L1** in-process ([cache/memory](../memory),
otter) → **L2** distributed ([cache/redis](../redis), rueidis) → origin loader,
with [singleflight](../singleflight) de-duplication.

read short-circuits at first tier that has key and back-fills faster tiers it skipped. write fans out to both tiers. Concurrent reads of same key share one loader call.
`cache.twotier.l2 = none` drops L2 (standalone, no Redis); same typed API + singleflight.
Optional cross-replica invalidation evicts peers' L1 on every Set / Delete / prefix invalidation.

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
| --- | --- |
| `twotier.Module` | fx module — provides `*twotier.TwoTier`; starts `Listen` on fx start |
| `twotier.New(l1, opts, logger, ...Option)` | constructor; `WithRedis(client)` for `l2 = redis`, `WithBroadcaster(b)` for invalidation |
| `Broadcaster` / `NewBusBroadcaster(bus, topic, logger)` | invalidation channel; bus adapter over `realtime/pubsub` (`pubsub/redis` reuses L2 server) |
| `(*TwoTier).Listen()` | subscribe to peer notices; returns stop func |
| `ErrBroadcast` | wraps failed broadcast; local tiers already changed |
| `twotier.NewTyped[V](tt, prefix)` | Type-safe view with a key prefix |
| `Typed.Get(ctx, k, loader)` | Read-through L1 → L2 → loader; back-fills tiers |
| `Typed.Set(ctx, k, v)` | Write-through to both tiers |
| `Typed.Delete(ctx, k)` | Removes from both tiers |
| `Typed.InvalidatePrefix(ctx, prefix)` | Bulk-evicts every key under the view + `prefix` from both tiers (`""` clears the whole view) |
| `(*TwoTier).InvalidatePrefix(ctx, prefix)` | Same, but takes an already-composed key prefix (no view prefix added) |

## Prefix invalidation

`InvalidatePrefix` composes prefix exactly like `Get`/`Set`/`Delete`
(`<view-prefix>:<prefix>`), then evicts from both tiers:

- **L1 (otter)** has no native prefix delete, so it is scanned with `Keys()` and
 matching entries are `Invalidate`d one by one. Mutation epochs fence older
 singleflight work. O(n) over live L1 set — fine for bounded in-process
 cache, not for huge keyspaces.
- **L2 (redis)** is cleared via `l2` adapter's `DelPrefix`: cursor-paged
 `SCAN MATCH "<prefix>*" COUNT 256` + batched `UNLINK` per page. `UNLINK`
 reclaims memory off main thread. adapter refuses empty composed
 prefix to avoid scanning whole keyspace.

## Cross-replica invalidation

- Each mutation sends one `Invalidation{Origin, Kind: key|prefix, Key}` — never per-key fan-out
  for a prefix. Receiver drops own origin, bumps epoch (fences in-flight loads), evicts L1 only;
  L2 already holds sender's write. Notices run synchronously in bus subscriber goroutine.
- Broadcast bounded by `invalidation.timeout`. Failure -> `Set` / `Delete` / `InvalidatePrefix`
  return error wrapping `ErrBroadcast` (+ cause); local change stays.
- Delivery at most once: lost, malformed or pre-subscription notice leaves peer L1 stale
  until `l1_ttl`. Hence invalidation requires `l1_ttl > 0`.
- Bus = `pubsub.GapBus` (redis): reconnect -> empty-prefix invalidation -> whole L1 evicted
  (#642). Notices sent during outage lost; flush = only safe catch-up.
- `pubsub.CheckedBus` (`TryPublish`) surfaces publish errors; plain `Bus` is fire-and-forget.

## Values cross tiers as JSON

L1 stores live Go value; L2 stores its JSON encoding (Redis is byte
store, and JSON keeps cache language-agnostic across replicas). Store
JSON-round-trippable values only. Invalid L2 JSON is deleted and repaired
through the origin loader.

## Disabled / nil-passthrough mode

`nil *TwoTier` is valid no-op cache. view built from nil (`NewTyped[V](nil,
…)`) calls the loader on every `Get` and makes `Set`/`Delete`/`InvalidatePrefix`
no-ops, so call sites never branch on whether caching is configured.

## Config

```ini
cache.twotier.l1_ttl = 1m   # positive value overrides L1 entry TTL; 0 inherits memory TTL
cache.twotier.l2_ttl = 5m   # L2 TTL, 0 = no expiry
cache.twotier.l2 = redis    # redis (needs rueidis.Client) | none (L1-only)
cache.twotier.invalidation.enabled = false  # needs pubsub.Bus + l1_ttl > 0
cache.twotier.invalidation.topic = golusoris.cache.twotier.invalidate
cache.twotier.invalidation.timeout = 2s
```

Negative TTLs, unknown `l2`, missing Redis client or bus fail module construction. Failed explicit Set/Delete leaves L1
unchanged. Successful mutation fences older in-flight loader cache writes.

## Don't

- Don't use real Redis in unit tests — L2 backend sits behind unexported `l2`
 interface; stub it (see `twotier_test.go`) and use `memory.NewForTest` for L1.
 Cross-replica Redis behaviour lives in `integration_test.go`.
- Don't rely on invalidation for correctness beyond `l1_ttl` — notices can be lost.
- Don't treat L2 outage as fatal — `Get` logs and falls through to  loader; only `Set`/`Delete` surface L2 errors (caller chose to write).
- Don't store values that don't JSON-round-trip — L2 holds JSON, not  live object.
- Don't cache by floating-point keys — keys are plain strings; format floats
 canonically before keying.
