<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — examples/full/

Runnable example: production-shaped golusoris app composing major
modules. Not library — `package main`, no exports. Copy it and remove modules you don't need.

## What it wires

```go
fx.New(
    golusoris.Core,        // config + log + clock + id + errors + validate + crypto
    golusoris.DB,          // pgx pool + migrations
    otel.Module,           // tracer + meter + OTLP
    golusoris.HTTP,        // chi router + HTTP server
    golusoris.K8s,         // pod metadata + Kubernetes client
    golusoris.Jobs,        // river client + worker registry
    golusoris.CacheMemory, // otter L1
    golusoris.CacheRedis,  // rueidis L2
    golusoris.AuthOIDC, authz.Module,   // PKCE OIDC + Casbin RBAC
    stripe.Module,                      // checkout + portal + intents
).Run()
```

Required config (env, `APP_` prefix): `APP_DB_DSN`, `APP_HTTP_ADDR`
(default `:8080`), `APP_CACHE_REDIS_ADDR` (default `localhost:6379`).

## Notes

- Demonstration surface — keep it in sync with exported `golusoris.*` fx
 vars and module set; it doubles as wiring documentation.
- Needs live Postgres + Redis (+ OIDC/Stripe creds) to `Run`; for  smaller smoke-test composition see `examples/minimal/`.
