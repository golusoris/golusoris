# ogen-go/ogen — v1.20.3 snapshot

Pinned: **v1.20.3**
Source: https://pkg.go.dev/github.com/ogen-go/ogen@v1.20.3
Docs: https://ogen.dev

## Code generation

```sh
go run github.com/ogen-go/ogen/cmd/ogen@v1.20.3 \
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
func (h *handler) GetUser(ctx context.Context, params api.GetUserParams) (*api.User, error) {
    return &api.User{ID: params.ID, Name: "Alice"}, nil
}

// Mount
srv, err := api.NewServer(handler{}, api.WithErrorHandler(myErrHandler))
http.Handle("/", srv)
```

## Error mapping

```go
// Return typed errors for RFC 9457 problem+json
func (h *handler) GetUser(ctx context.Context, params api.GetUserParams) (*api.User, error) {
    if notFound {
        return nil, &api.ErrorStatusCode{
            StatusCode: http.StatusNotFound,
            Response:   api.Error{Message: "user not found"},
        }
    }
    return nil, err
}
```

## Security handler

```go
type secHandler struct{}
func (s *secHandler) HandleBearerAuth(ctx context.Context, op api.OperationName, t api.BearerAuth) (context.Context, error) {
    // validate t.Token
    return ctx, nil
}
srv, _ := api.NewServer(h, api.WithSecurityHandler(secHandler{}))
```

## golusoris usage

- `ogenkit/` — error handler (RFC 9457), recovery middleware, chi adapter.
- `apidocs/` — Scalar UI + MCP server mount alongside ogen-generated server.

## Links

- Changelog: https://github.com/ogen-go/ogen/blob/main/CHANGELOG.md
- Examples: https://github.com/ogen-go/ogen/tree/main/_examples
