# Agent guide — idempotency/

HTTP middleware enforcing the Idempotency-Key header
(draft-ietf-httpapi-idempotency-key-header). Captures the first response for
a key and replays it verbatim on subsequent requests, without re-invoking the
handler.

## Core types

| Type | Purpose |
|---|---|
| `CachedResponse` | Stored representation: StatusCode, Header, Body |
| `Store` | `Find` + `Save` — implement with Redis or Postgres; `MemoryStore` for tests |
| `Options` | Header name, TTL, Required flag |
| `Middleware(store, opts)` | Wraps non-safe methods (POST/PUT/PATCH/DELETE) |

## Behaviour

- GET/HEAD/OPTIONS pass through unchanged.
- 5xx responses are **not** cached (transient failures can be retried).
- 2xx and 4xx responses are cached for `Options.TTL` (default 24h).
- Missing key: pass through (unless `Options.Required = true`, then 400).

## Usage

```go
mux.Handle("/payments", idempotency.Middleware(store, idempotency.Options{
    Required: true,
    TTL:      48 * time.Hour,
})(paymentHandler))
```

## Don't

- Don't use `MemoryStore` in multi-replica deployments — keys won't be shared.
- Don't set `Required: true` on endpoints that already handle idempotency
  internally via unique DB constraints.
