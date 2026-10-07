<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — graphql/client/

fx-wired wrapper around genqlient GraphQL **client** for consuming external
GraphQL APIs. Provides `graphql.Client` (genqlient) that generated typed
query functions call. server-side counterpart is `graphql/`.

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

genqlient generates typed funcs from `.graphql` queries + schema. Add `genqlient.yaml` at app root and run `go run github.com/Khan/genqlient`;
generated code calls `graphql.Client` this module provides.

## Config

Keys under `graphql.client` prefix (env `APP_GRAPHQL_CLIENT_*`):

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

- Auth headers are injected per-request via cloning `RoundTripper` ( original request is not mutated).
- `endpoint` is mandatory — missing it returns error at construction, not
 first call.
- `*http.Client` always sets `Timeout` (CI rule `http-client-must-set-timeout`).
