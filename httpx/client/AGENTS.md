# Agent guide — httpx/client

Builds outbound `*http.Client` with retry, circuit breaker, and OTel spans.

## Layering (outer → inner)

```
circuit-breaker → retry → otelhttp → stdlib transport
```

- **Circuit breaker outermost** so an open breaker short-circuits without entering the retry loop. 5xx + network errors trip the breaker; 4xx do not.
- **Retry** uses hashicorp/go-retryablehttp — retries on 429/5xx + network errors with exponential backoff + jitter.
- **OTel** emits one span per HTTP request (per retry attempt).

## Conventions

- Apps calling third-party APIs build a client per upstream with a distinctive `Name` — breaker state-change logs + OTel spans then tell you which dependency is flaky.
- `Timeout` covers the entire request lifecycle (including redirects + body read). For long-running endpoints, prefer `ctx` deadlines.
- `Drain(ctx, resp)` is the canonical way to return a response body to the connection pool after an early error path.

## Don't

- Don't use `http.DefaultClient` for external calls. No timeout + no retries + no tracing = production pain.
- Don't set `Retry.Max` very high (>5) — retries compound latency under load. Prefer breaker + circuit-breaker for long outages.
