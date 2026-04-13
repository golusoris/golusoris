# Agent guide — httpx/

The HTTP stack. Opt-in via `golusoris.HTTP` (or individual modules).

| Subpackage | Purpose |
|---|---|
| `httpx/server` | `*http.Server` fx module with timeouts, body limits, graceful shutdown |
| `httpx/router` | chi router as `chi.Router` + `http.Handler` |
| `httpx/middleware` | RequestID, Recover, Logger, OTel, SecureHeaders, TrustProxy, Compress, ETag |
| `httpx/client` | Retrying OTel-instrumented outbound client (Step 3b) |

## Conventions

- `httpx/server` expects a `http.Handler` in the fx graph. `httpx/router.Module` provides one; apps can also provide their own.
- Middleware are plain `func(http.Handler) http.Handler` values. Compose via `middleware.Chain`.
- `RequestID` runs first so `Logger` / `Recover` can include the ID in logs.
- `TrustProxy` must run before any middleware that reads `r.RemoteAddr` for trust decisions (rate limit, geofence).
- OTel middleware accepts a `trace.TracerProvider`. Apps without OTel can pass `nil` — the global no-op provider is used.

## Don't

- Don't read `r.Header.Get("X-Forwarded-For")` directly from app code. Use `middleware.TrustProxy` + `r.RemoteAddr`.
- Don't write access logs from handlers — `middleware.Logger` emits one structured entry per request.
