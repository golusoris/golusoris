<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/ratelimit

Per-IP rate limit via ulule/limiter/v3. Process-local memory store by default.

## Conventions

- `http.ratelimit.rate` grammar: `"100-M"` = 100/minute, `"5-S"` = 5/second, `"1-H"` = 1/hour, `"100-D"` = 100/day. Empty rate = pass-through.
- Peer IP comes from `r.RemoteAddr`. If app is behind reverse proxy, run `middleware.TrustProxy` first so real client IP is limited (not proxy).
- `trust_xff=true` is rejected. Run CIDR-gated `TrustProxy` first.

## Scaling

- Multi-replica deploys require shared `limiter.Store` in `Options.Store`.
- Set store directly or decorate `ratelimit.Options` in fx graph.
- `cache/redis` provides `rueidis.Client`, not a limiter store. Add explicit adapter or use compatible upstream driver.

## Don't

- Don't use this for auth brute-force protection. `auth/lockout` is correct primitive there.
