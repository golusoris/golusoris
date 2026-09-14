- HISS burn-down (group `httpx`: `apidocs`, `grpc`, `httpx/*`, `idempotency`, `tenancy`) — no more blank-identifier discards or production `panic`s; `grpc.newServer` split into helpers.
  - **BREAKING** `tenancy`: `MustFromContext` (panicked) is replaced by `RequireFromContext`, which returns the new sentinel `ErrMissingTenant`.

    ```go
    // before
    t := tenancy.MustFromContext(ctx) // panics when no tenant

    // after
    t, err := tenancy.RequireFromContext(ctx)
    if errors.Is(err, tenancy.ErrMissingTenant) { /* handler mounted outside Middleware */ }
    ```

  - `idempotency.Options` gains `Logger *slog.Logger` (nil → `slog.Default()`); `Store.Save` failures and replay write errors are now logged instead of silently dropped. `idempotency.Module` wires the fx `*slog.Logger` automatically.

    ```go
    // before
    idempotency.Middleware(store, idempotency.Options{TTL: time.Hour})

    // after (optional — nil Logger keeps the old call shape working)
    idempotency.Middleware(store, idempotency.Options{TTL: time.Hour, Logger: logger})
    ```

  - `httpx/client.Drain` now logs drain/close failures at Debug on `slog.Default()` (signature unchanged).
  - `httpx/extclient`: a failed response-body close now surfaces as the call error when the request otherwise succeeded.
  - `httpx/geofence.Module`: the fx `OnStop` hook returns the mmdb reader's close error instead of swallowing it.
  - `apidocs` `/mcp` tool proxy: a failed response-body close is returned as the tool-call error instead of being dropped.
