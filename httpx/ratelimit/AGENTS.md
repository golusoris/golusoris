# Agent guide — httpx/ratelimit

Per-IP rate limit via ulule/limiter/v3 with an in-memory store.

## Conventions

- `http.ratelimit.rate` grammar: `"100-M"` = 100/minute, `"5-S"` = 5/second, `"1-H"` = 1/hour, `"100-D"` = 100/day. Empty rate = pass-through.
- Peer IP comes from `r.RemoteAddr`. If the app is behind a reverse proxy, run `middleware.TrustProxy` first so the real client IP is limited (not the proxy).
- `http.ratelimit.trust_xff=true` makes ulule's own X-Forwarded-For extraction kick in. Prefer `TrustProxy` — it's CIDR-gated; `trust_xff` trusts any sender.

## Scaling

- Multi-replica deploys share a store via `cache/redis` (Step 8). Apps swap stores via `fx.Decorate` once the redis module is in.

## Don't

- Don't use this for auth brute-force protection — `auth/lockout` (Step 9) is the correct primitive there.
