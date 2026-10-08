<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# maypok86/otter/v2 — v2.3.0 snapshot

Pinned: **v2.3.0**
Source: [tagged source](https://github.com/maypok86/otter/tree/v2.3.0)

## Building a cache

```go
import (
    "github.com/maypok86/otter/v2"
    "github.com/maypok86/otter/v2/stats"
)

c, err := otter.New(&otter.Options[string, User]{
    MaximumSize:      10_000,
    ExpiryCalculator: otter.ExpiryWriting[string, User](5 * time.Minute),
    StatsRecorder:    stats.NewCounter(),
})
```

## Operations

```go
// Set; update this entry's configured expiry when needed.
previous, replaced := c.Set("user:42", user)
c.SetExpiresAfter("user:42", 30*time.Second)

// Cache-only lookup. Get is the loader-oriented operation.
val, ok := c.GetIfPresent("user:42")

// Invalidate
value, invalidated := c.Invalidate("user:42")

// Invalidate all entries
c.InvalidateAll()

// Terminal shutdown for cache-owned goroutines
stopped := c.StopAllGoroutines()
```

## Stats

```go
stats := c.Stats()
stats.Hits
stats.Misses
stats.HitRatio()
stats.MissRatio()
```

## golusoris usage

- `cache/memory/` — typed `*otter.Cache[K, V]` provided via Fx; capacity and
  TTL come from configuration.

## Notes

- Otter v2 uses adaptive W-TinyLFU admission and eviction.
- Prefer `otter.New` in constructors so invalid options fail Fx startup cleanly.
  `otter.Must` panics on invalid options and is appropriate only for static,
  already-validated configuration.

## Links

- [Changelog](https://github.com/maypok86/otter/blob/v2.3.0/CHANGELOG.md)
