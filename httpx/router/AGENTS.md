<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/router

Thin adapter exposing `*chi.Mux` as `chi.Router` + `http.Handler` via fx.

## Conventions

- Apps mount routes via `fx.Invoke(func(r chi.Router) { r.Mount("/api", apiHandler) })`.
- For non-ogen routes (admin UI, webhooks, static) use `r.Get/Post/…` directly. ogen-generated handlers mount via `r.Mount("/api", ogenServer)` — see `ogenkit/`.
- Middleware goes on router via `r.Use(...)` or per-subroute via `r.Group`.

## Don't

- Don't instantiate separate `http.ServeMux` alongside chi router. server resolves one `http.Handler`; multiple routers fragment middleware + metrics.
