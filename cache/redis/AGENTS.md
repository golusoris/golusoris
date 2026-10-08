<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — cache/redis/

[rueidis](https://github.com/redis/rueidis) client as fx module. `InitAddress`
auto-detects standalone versus cluster. Wrapper does not configure Sentinel or
opt in to client-side caching.

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

```ini
cache.redis.addr = "localhost:6379"   # comma-separated for cluster
cache.redis.user = ""
cache.redis.pass = ""
cache.redis.db   = 0                  # standalone only
cache.redis.tls  = false
```

`addr` stays bare `host:port`; `tls = true` enables verified TLS 1.2+.
Connection URLs belong at integration boundaries, not in `cache.redis.addr`.

## Distributed locks

rueidis ships `rueidislock` — use it rather than hand-rolling SETNX. Import
`github.com/redis/rueidis/rueidislock` directly; no extra fx module needed.

## Don't

- Don't use `redis.Module` in tests — use `testutil/redis.Start(t)` for fresh container.
- Don't call `client.Close()` manually — fx lifecycle hook handles it.
- Don't use `Do` in hot paths without pipeline — `DoMulti` or `Pipelined` for batch ops.
