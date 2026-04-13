# Agent guide — cache/singleflight/

Typed wrapper over [golang.org/x/sync/singleflight]. De-duplicates
concurrent calls with the same key so exactly one goroutine hits the
backing store while the others wait and share the result.

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
- NOT a replacement for a cache — entries aren't retained; every new wave of concurrent calls runs fn once.

## Don't

- Don't pass request-scoped data via ctx into fn — context belongs to the first caller; later callers share the result, not the context.
- Don't use as a rate-limiter — singleflight collapses concurrent identical calls, not sequential ones.
