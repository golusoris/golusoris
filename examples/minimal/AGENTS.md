# Agent guide — examples/minimal/

Runnable example: the smallest useful golusoris app — five modules. Not a
library — `package main`, no exports. The minimal counterpart to
`examples/full/`.

## What it wires

```go
fx.New(
    golusoris.Core, // config + log + lifecycle + errors + clock + id
    golusoris.DB,   // pgx pool + migrations + sqlc helpers
    otel.Module,    // tracer + meter + logs + OTLP
    golusoris.HTTP, // server + standard middleware + Scalar docs
    golusoris.K8s,  // /livez /readyz /startupz + podinfo + prom /metrics
).Run()
```

Run:

```sh
export APP_HTTP_ADDR=":8080"
export APP_DB_DSN="postgres://..."
go run github.com/golusoris/golusoris/examples/minimal
```

## Notes

- Demonstration surface — keep in sync with the `golusoris.Core/DB/HTTP/K8s`
  fx vars and the `otel.Module` name.
- Still needs a reachable Postgres (`APP_DB_DSN`) to start; for the full module
  matrix see `examples/full/`.
