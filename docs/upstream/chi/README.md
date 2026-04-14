# go-chi/chi/v5 — v5.2.1 snapshot

Pinned: **v5.2.1**
Source: https://pkg.go.dev/github.com/go-chi/chi/v5@v5.2.1

## Router

```go
import "github.com/go-chi/chi/v5"

r := chi.NewRouter()

// Middleware (applied in order)
r.Use(middleware.RequestID)
r.Use(middleware.RealIP)
r.Use(middleware.Logger)
r.Use(middleware.Recoverer)

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
middleware.Logger
middleware.Recoverer
middleware.Compress(5)
middleware.StripSlashes
middleware.Timeout(30 * time.Second)
middleware.BasicAuth("realm", map[string]string{"user": "pass"})
```

## golusoris usage

- `httpx/router/` — `chi.Router` provided via fx; ogen server mounted on it.
- `httpx/middleware/` — wraps chi middleware + custom golusoris middleware.

## Links

- Changelog: https://github.com/go-chi/chi/blob/master/CHANGELOG.md
