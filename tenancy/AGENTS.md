<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — tenancy/

Multi-tenant context propagation and HTTP middleware. `Tenant` is resolved
from request (header, subdomain, JWT claim, …) and stored in context.

## Core types

| Type/Func | Purpose |
| --- | --- |
| `Tenant` | ID, Slug, Plan, Metadata |
| `Store` | `FindByID` + `FindBySlug` — implement with Postgres |
| `MemoryStore.Add` | Upsert by non-empty ID; lowercase unique slug; stale slug removed |
| `ExtractFunc` | `func(*http.Request) (TenantRef, error)`; ref kind selects ID or slug lookup |
| `Middleware(extract, store)` | Resolves + stores tenant; 400 malformed, 401 unknown, 500 unavailable |
| `FromContext(ctx)` | Returns `(Tenant, bool)` |
| `RequireFromContext(ctx)` | Returns `(Tenant, error)`; `ErrMissingTenant` when no tenant — use behind Middleware |
| `HeaderExtractor(header)` | Reads tenant ID from named header |
| `SubdomainExtractor(base)` | Reads one canonical subdomain label (e.g. `acme.example.com` → `acme`) |

## Usage

```go
extract := tenancy.SubdomainExtractor("example.com")
mux.Use(tenancy.Middleware(extract, store))

// In handler:
t, ok := tenancy.FromContext(r.Context())
```

## Don't

- Don't call `RequireFromContext` outside handlers guarded by `Middleware`
 (it returns `ErrMissingTenant` rather than panicking, but that is still  wiring bug).
- Don't store tenant ID directly in JWT claims without verifying it
 against DB — use store on every request.
