# Agent guide — httpx/middleware

HTTP middleware toolkit. Each middleware is a plain `func(http.Handler) http.Handler`.

## Order

The canonical stack, outermost first:

```go
r.Use(
    middleware.RequestID,                    // sets X-Request-ID on ctx+response
    middleware.TrustProxy(trustedCIDRs),     // rewrites RemoteAddr from trusted proxies
    middleware.Recover(logger),              // catches panics → 500
    middleware.Logger(logger, clk),          // structured access log
    middleware.OTel("app", tracerProvider),  // span per request
    middleware.SecureHeaders(middleware.SecureHeadersDefaults()),
    compress,                                // built via middleware.Compress()
    middleware.ETag,                         // weak ETag + 304 for GET
)
```

## Conventions

- All middleware respect the request context — panics log with the X-Request-ID for correlation.
- OTel middleware accepts `trace.TracerProvider`; `nil` falls back to `otel.GetTracerProvider()` (no-op unless the app wires a real one).
- `TrustProxy` gates rewriting by direct-peer CIDR — *never* read `X-Forwarded-For` from app code.
- `ETag` buffers the response body in memory; skip for streaming endpoints.

## Don't

- Don't write new middleware that reads `r.RemoteAddr` for trust decisions unless it runs *after* `TrustProxy`.
- Don't log access lines from handlers — the Logger middleware emits one per request with status, bytes, elapsed, and request_id.
