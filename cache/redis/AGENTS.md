# Agent guide — cache/redis/

[rueidis](https://github.com/redis/rueidis) client as an fx module. rueidis
auto-detects standalone vs cluster from `InitAddress` and supports client-side
caching (CSC) without extra config.

## Usage

```go
// Wire (provides rueidis.Client):
fx.New(golusoris.Core, redis.Module)

// Use in a service:
func NewRateLimiter(r rueidis.Client) *RateLimiter {
    cmd := r.B().Set().Key("foo").Value("bar").Build()
    _ = r.Do(ctx, cmd).Error()
}
```

## Config

```
cache.redis.addr = "localhost:6379"   # comma-sep for cluster/sentinel
cache.redis.user = ""
cache.redis.pass = ""
cache.redis.db   = 0                  # standalone only
cache.redis.tls  = false
```

## Distributed locks

rueidis ships `rueidislock` — use it rather than hand-rolling SETNX. Import
`github.com/redis/rueidis/rueidislock` directly; no extra fx module needed.

## Don't

- Don't use `redis.Module` in tests — use `testutil/redis.Start(t)` for a fresh container.
- Don't call `client.Close()` manually — the fx lifecycle hook handles it.
- Don't use `Do` in hot paths without pipeline — `DoMulti` or `Pipelined` for batch ops.
