<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/

HTTP middleware enforcing Idempotency-Key header
(draft-ietf-httpapi-idempotency-key-header). Captures first response for
key and replays it verbatim on subsequent requests, without re-invoking handler.

## Core types

| Type | Purpose |
|---|---|
| `CachedResponse` | Stored representation: StatusCode, Header, Body |
| `Store` | Atomic fingerprinted `Claim` + token-bound `Commit` / `Release`; Redis or Postgres; `MemoryStore` for tests |
| `Options` | Header, TTL, Required, 1 MiB request/response bounds, optional principal Scope from `NewScopeFunc`, Logger |
| `Middleware(store, opts)` | Wraps non-safe methods (POST/PUT/PATCH/DELETE) |

## Behaviour

- GET/HEAD/OPTIONS pass through unchanged.
- 5xx responses are **not** cached (transient failures can be retried).
- 2xx and 4xx responses are cached for `Options.TTL` (default 24h).
- Missing key: pass through (unless `Options.Required = true`, then 400).
- Scope: method + host + canonical target + tenancy ID + optional principal.
- Same scoped key in flight: 409. Different payload after completion: 422.
- Request fingerprint overflow: 413. Response capture overflow: deliver, release, skip cache.

## Usage

```go
mux.Handle("/payments", idempotency.Middleware(store, idempotency.Options{
    Required: true,
    TTL:      48 * time.Hour,
    Scope: idempotency.NewScopeFunc(func(r *http.Request) (string, error) {
        return authenticatedPrincipal(r), nil
    }),
})(paymentHandler))
```

## Don't

- Don't use `MemoryStore` in multi-replica deployments — keys won't be shared.
- Don't set `Required: true` on endpoints that already handle idempotency
 internally via unique DB constraints.
