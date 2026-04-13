# Agent guide — httpx/csrf

Double-submit cookie CSRF via gorilla/csrf.

## Conventions

- `http.csrf.secret` must decode to 32 bytes (hex or base64). Apps generate one per-deployment, rotated via the usual secret-management workflow. No secret → middleware is a no-op.
- In templates: `<input name="gorilla.csrf.Token" value="{{ csrf.Token . }}">`. For SPAs: read `X-CSRF-Token` from a preceding GET + echo on unsafe requests.
- Cookie is `Secure` by default — disable only for localhost dev via `http.csrf.secure=false`.

## Don't

- Don't also enable CSRF for API-token-authenticated endpoints — the bearer token already proves intent. Route those past the middleware via a separate sub-router.
