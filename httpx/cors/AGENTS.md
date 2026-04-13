# Agent guide — httpx/cors

CORS middleware wrapping rs/cors. Denies cross-origin by default.

## Conventions

- Explicit allowlist: `http.cors.origins` must be set. `*` is the only non-list wildcard and should be avoided when credentials are enabled.
- Mount near the top of the middleware stack so preflight OPTIONS short-circuit before auth + rate limit.

## Don't

- Don't enable `credentials=true` with `origins=["*"]` — browsers reject it, and it's a CSRF vector.
