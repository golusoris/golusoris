<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/ratelimit

Per-IP rate limit via ulule/limiter/v3. Process-local memory store by default.

## Conventions

- `http.ratelimit.rate` grammar: `"100-M"` = 100/minute, `"5-S"` = 5/second, `"1-H"` = 1/hour, `"100-D"` = 100/day. Empty rate = pass-through.
- Peer IP from `r.RemoteAddr`. Behind reverse proxy: run `middleware.TrustProxy` first; limit real client IP, not proxy.
- `trust_xff=true` rejected. Run CIDR-gated `TrustProxy` first.

## Scaling

- Multi-replica deploys require shared `limiter.Store` in `Options.Store`.
- Set store directly or decorate `ratelimit.Options` in fx graph.
- `cache/redis` provides `rueidis.Client`, not limiter store. Add explicit adapter or use compatible upstream driver.

## Don't

- Auth brute-force protection -> `auth/lockout`, not this package.

## Outbound

- `OutboundModule`: independent provider; `http.outbound.ratelimit.{rate,hosts,max_wait}`.
- Hosts: slice of host/rate; exact lowercase hostname, no port; empty rate unlimited.
- Cache outside limiter; hits consume no quota; injected clock controls bounded waits.
- MaxWait zero: fail fast; at most two store checks; ErrLimited sentinel.
- Scripted store in tests; memory store uses wall clock.
