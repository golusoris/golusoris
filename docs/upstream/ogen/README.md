<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ogen-go/ogen — v1.24.0 snapshot

Pinned: **v1.24.0**
Source: [tagged source](https://github.com/ogen-go/ogen/tree/v1.24.0)
Docs: [Ogen documentation](https://ogen.dev)

## Code generation

```sh
go run github.com/ogen-go/ogen/cmd/ogen@v1.24.0 \
  --target ./internal/api \
  --clean \
  openapi.yaml
```

Generates:

- `oas_server_gen.go` — `Handler` interface (one method per operation)
- `oas_router_gen.go` — `*Server` with `ServeHTTP`
- `oas_schemas_gen.go` — request/response types
- `oas_client_gen.go` — typed client

## Implementing the server

```go
type handler struct{}

// Implement every method in the generated Handler interface.
func (h *handler) GetUser(
    ctx context.Context,
    params api.GetUserParams,
) (*api.User, error) {
    return &api.User{ID: params.ID, Name: "Alice"}, nil
}

// Mount. A generated server without security takes the handler plus options.
srv, err := api.NewServer(
    handler{},
    api.WithErrorHandler(ogenkit.ErrorHandler(logger)),
)
if err != nil {
    return fmt.Errorf("new API server: %w", err)
}
r.Mount("/", srv)
```

## Error mapping

Declared operation failures are generated response variants. Their Go names
come from the OpenAPI document; `api.ErrorStatusCode` is not a generic Ogen
type. Return the generated not-found/problem response for an expected domain
failure. Use the generated `api.WithErrorHandler` option for decode, transport,
and unexpected handler failures; Golusoris provides `ogenkit.ErrorHandler` for
that boundary.

## Security handler

Security interfaces and method names are generated from the OpenAPI security
scheme. When a schema generates `SecurityHandler`, pass its implementation as
the second positional argument:

```go
srv, err := api.NewServer(
    handler{},
    securityHandler{},
    api.WithErrorHandler(ogenkit.ErrorHandler(logger)),
)
```

There is no generated `api.WithSecurityHandler` option in v1.24.0. Handle the
returned construction error before mounting `srv`.

## golusoris usage

- `ogenkit/` — error handler (RFC 9457), recovery middleware, chi adapter.
- `apidocs/` — Scalar UI + MCP server mount alongside ogen-generated server.

## Links

- [Tagged source](https://github.com/ogen-go/ogen/tree/v1.24.0)
- [Ogen documentation](https://ogen.dev)
