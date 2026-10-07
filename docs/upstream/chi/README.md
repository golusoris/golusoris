<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# go-chi/chi/v5 — v5.3.2 snapshot

Pinned: **v5.3.2**
Source: [tagged source](https://github.com/go-chi/chi/tree/v5.3.2)

## Router

```go
import "github.com/go-chi/chi/v5"

r := chi.NewRouter()

// Middleware (applied in order)
r.Use(middleware.RequestID)
r.Use(middleware.RealIP)
r.Use(middleware.Recoverer)
r.Use(httpxmiddleware.Logger(logger, clk))

// Routes
r.Get("/", handler)
r.Post("/users", createUser)
r.Put("/users/{id}", updateUser)
r.Delete("/users/{id}", deleteUser)

// URL params
func handler(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
}
```

## Sub-routers and groups

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Use(authMiddleware)
    r.Get("/users", listUsers)
    r.Post("/users", createUser)
    r.Route("/users/{id}", func(r chi.Router) {
        r.Get("/", getUser)
        r.Put("/", updateUser)
    })
})

// Mount sub-router
r.Mount("/admin", adminRouter())
```

## Middleware

```go
// Built-in middleware
middleware.RequestID
middleware.RealIP
middleware.Recoverer
middleware.Compress(5)
middleware.StripSlashes
middleware.Timeout(30 * time.Second)
middleware.BasicAuth("realm", map[string]string{"user": "pass"})
```

## golusoris usage

- `httpx/router/` — `chi.Router` provided via fx; ogen server mounted on it.
- `httpx/middleware/` — bounded recovery and slog-backed access logging. Prefer
  it over chi's standard-library `middleware.Logger` in Golusoris applications.

## Links

- [Changelog](https://github.com/go-chi/chi/blob/v5.3.2/CHANGELOG.md)
