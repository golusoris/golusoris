# Agent guide — httpx/router

Thin adapter exposing a `*chi.Mux` as `chi.Router` + `http.Handler` via fx.

## Conventions

- Apps mount routes via `fx.Invoke(func(r chi.Router) { r.Mount("/api", apiHandler) })`.
- For non-ogen routes (admin UI, webhooks, static) use `r.Get/Post/…` directly. ogen-generated handlers mount via `r.Mount("/api", ogenServer)` — see `ogenkit/` (Step 3c).
- Middleware goes on the router via `r.Use(...)` or per-subroute via `r.Group`.

## Don't

- Don't instantiate a separate `http.ServeMux` alongside the chi router. The server resolves one `http.Handler`; multiple routers fragment middleware + metrics.
