<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — examples/minimal/

Runnable example: smallest useful golusoris app — five modules. Serves `/livez` + `/readyz` + `/startupz` (`/readyz` pings Postgres); needs Postgres. Not library — `package main`, no exports. minimal counterpart to
`examples/full/`.

## What it wires

```go
fx.New(
    golusoris.Core, // config + log + clock + id + validate + crypto
    golusoris.DB,   // pgx pool + migrations
    otel.Module,    // tracer + meter + logs + OTLP
    golusoris.HTTP, // chi router + HTTP server
    golusoris.K8s,  // pod metadata + Kubernetes client
    fx.Provide(statuspage.NewRegistry), health.Module, dbpgx.ReadinessModule,
    fx.Invoke(mountHealth),           // /livez /readyz /startupz on the router
    fx.Invoke(func(*http.Server) {}), // consume the server so its listen hook registers
).Run()
```

Run:

```sh
export APP_HTTP_ADDR=":8080"
export APP_DB_DSN="postgres://..."
export APP_K8S_KUBECONFIG="/path/to/kubeconfig" # outside a cluster
go run github.com/golusoris/golusoris/examples/minimal
```

## Notes

- Demonstration surface — keep in sync with `golusoris.Core/DB/HTTP/K8s`
 fx vars and `otel.Module` name.
- Still needs reachable Postgres (`APP_DB_DSN`) to start; for full module
 matrix see `examples/full/`.
- `sizing_test.go` (build tag `sizing`, `make pulumi-sizing-test`) runs this binary under load and asserts peak RSS and CPU stay within Pulumi Fargate task size.
