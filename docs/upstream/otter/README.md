# maypok86/otter/v2 — v2.2.0 snapshot

Pinned: **v2.2.0**
Source: https://pkg.go.dev/github.com/maypok86/otter/v2@v2.2.0

## Building a cache

```go
import "github.com/maypok86/otter/v2"

c, err := otter.MustBuilder[string, User](10_000).
    TTL(5 * time.Minute).
    Build()
```

## Operations

```go
// Set
c.Set("user:42", user)
c.SetWithTTL("user:42", user, 30*time.Second)

// Get
val, ok := c.Get("user:42")

// Delete
c.Delete("user:42")

// Clear
c.Clear()

// Close (stop background goroutines)
c.Close()
```

## Stats

```go
stats := c.Stats()
stats.Hits()
stats.Misses()
stats.Ratio()
```

## golusoris usage

- `cache/memory/` — typed `*otter.Cache[K, V]` provided via fx; capacity + TTL from config.

## Notes

- Otter uses the S3-FIFO eviction algorithm. No `sync.Map` or mutex per entry.
- `MustBuilder` panics on invalid config — use in `fx.Provide` (startup phase only).

## Links

- Changelog: https://github.com/maypok86/otter/blob/main/CHANGELOG.md
