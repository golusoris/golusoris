<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — cache/singleflight/

Typed wrapper over [golang.org/x/sync/singleflight]. De-duplicates
concurrent calls with same key so exactly one goroutine hits backing store while others wait and share result.

Generic key identity uses Go equality. No string formatting or cross-key collision.

## Usage

```go
// Construct directly (no fx module — it's stateless):
g := singleflight.New[string, *User]()

// In a handler:
user, shared, err := g.Do(ctx, userID, func(ctx context.Context) (*User, error) {
    return db.LoadUser(ctx, userID)
})
_ = shared // true if this goroutine shared someone else's in-flight call
```

## When to use

- Database reads that may get concurrent identical queries (user profile, config).
- Expensive computations (thumbnail generation, heavy aggregations).
- NOT replacement for cache — entries aren't retained; every new wave of concurrent calls runs fn once.

## Don't

- Don't pass request-scoped data via ctx into fn — context belongs to first caller; later callers share result, not context.
- Don't use as rate-limiter — singleflight collapses concurrent identical calls, not sequential ones.
