# Agent guide — examples/full/

Runnable example: a production-shaped golusoris app composing the major
modules. Not a library — `package main`, no exports. Copy it and remove the
modules you don't need.

## What it wires

```go
fx.New(
    golusoris.Core,        // config + log + clock + id + errors + validate + crypto
    golusoris.DB,          // pgx pool + migrations + sqlc
    otel.Module,           // tracer + meter + OTLP
    golusoris.HTTP,        // server + middleware + Scalar docs
    golusoris.K8s,         // /livez /readyz /startupz + /metrics
    golusoris.Jobs,        // river queue + cron
    golusoris.CacheMemory, // otter L1
    golusoris.CacheRedis,  // rueidis L2
    golusoris.AuthOIDC, authz.Module,   // PKCE OIDC + Casbin RBAC
    stripe.Module,                      // checkout + portal + intents
).Run()
```

Required config (env, `APP_` prefix): `APP_DB_DSN`, `APP_HTTP_ADDR`
(default `:8080`), `APP_CACHE_REDIS_ADDR` (default `localhost:6379`).

## Notes

- Demonstration surface — keep it in sync with the exported `golusoris.*` fx
  vars and module set; it doubles as wiring documentation.
- Needs live Postgres + Redis (+ OIDC/Stripe creds) to actually `Run`; for the
  smaller smoke-test composition see `examples/minimal/`.
