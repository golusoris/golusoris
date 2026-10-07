<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — examples/minimal/

Runnable example: smallest useful golusoris app — five modules. Not library — `package main`, no exports. minimal counterpart to
`examples/full/`.

## What it wires

```go
fx.New(
    golusoris.Core, // config + log + clock + id + validate + crypto
    golusoris.DB,   // pgx pool + migrations
    otel.Module,    // tracer + meter + logs + OTLP
    golusoris.HTTP, // chi router + HTTP server
    golusoris.K8s,  // pod metadata + Kubernetes client
).Run()
```

Run:

```sh
export APP_HTTP_ADDR=":8080"
export APP_DB_DSN="postgres://..."
go run github.com/golusoris/golusoris/examples/minimal
```

## Notes

- Demonstration surface — keep in sync with `golusoris.Core/DB/HTTP/K8s`
 fx vars and `otel.Module` name.
- Still needs reachable Postgres (`APP_DB_DSN`) to start; for full module
 matrix see `examples/full/`.
