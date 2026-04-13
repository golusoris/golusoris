# Agent guide — tenancy/

Multi-tenant context propagation and HTTP middleware. A `Tenant` is resolved
from the request (header, subdomain, JWT claim, …) and stored in context.

## Core types

| Type/Func | Purpose |
|---|---|
| `Tenant` | ID, Slug, Plan, Metadata |
| `Store` | `FindByID` + `FindBySlug` — implement with Postgres |
| `ExtractFunc` | `func(*http.Request) (id string, err error)` |
| `Middleware(extract, store)` | Resolves + stores tenant; 400 on bad extract, 401 on unknown |
| `FromContext(ctx)` | Returns `(Tenant, bool)` |
| `MustFromContext(ctx)` | Panics when no tenant — use only behind Middleware |
| `HeaderExtractor(header)` | Reads tenant ID from named header |
| `SubdomainExtractor(base)` | Reads first subdomain label (e.g. `acme.example.com` → `acme`) |

## Usage

```go
extract := tenancy.SubdomainExtractor("example.com")
mux.Use(tenancy.Middleware(extract, store))

// In handler:
t, ok := tenancy.FromContext(r.Context())
```

## Don't

- Don't call `MustFromContext` outside handlers guarded by `Middleware`.
- Don't store the tenant ID directly in JWT claims without verifying it
  against the DB — use the store on every request.
