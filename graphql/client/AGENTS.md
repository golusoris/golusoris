# Agent guide — graphql/client/

fx-wired wrapper around the genqlient GraphQL **client** for consuming external
GraphQL APIs. Provides a `graphql.Client` (genqlient) that generated typed
query functions call. The server-side counterpart is `graphql/`.

## Wiring

```go
fx.New(
    client.Module, // provides graphql.Client (genqlient)
    fx.Invoke(func(c graphql.Client) {
        // resp, err := generated.GetUser(ctx, c, userID)
    }),
)
```

**Provides** `graphql.Client`. **Requires** `*config.Config`. Construction
fails if `endpoint` is unset.

## Code generation

genqlient generates typed funcs from `.graphql` queries + a schema. Add a
`genqlient.yaml` at the app root and run `go run github.com/Khan/genqlient`;
the generated code calls the `graphql.Client` this module provides.

## Config

Keys under the `graphql.client` prefix (env `APP_GRAPHQL_CLIENT_*`):

```yaml
graphql:
  client:
    endpoint: https://api.example.com/graphql   # required
    timeout: 30s                                  # per-request HTTP timeout
    bearer_token: "..."                           # -> Authorization: Bearer
    api_key: "..."                                # -> X-Api-Key
    websocket: false                              # WS transport for subscriptions
```

## Notes

- Auth headers are injected per-request via a cloning `RoundTripper` (the
  original request is not mutated).
- `endpoint` is mandatory — missing it returns an error at construction, not
  first call.
- The `*http.Client` always sets `Timeout` (CI rule `http-client-must-set-timeout`).
