# Agent guide — authz/

RBAC/ABAC policy enforcement via [casbin/casbin/v2]. Module provides
`*authz.Enforcer` when `authz.Options` is supplied in the fx graph.

## Usage

```go
fx.New(
    golusoris.Core,
    fx.Supply(authz.Options{
        Model:   authz.ModelRBAC,
        Adapter: authz.NewFileAdapter("policy.csv"),
    }),
    authz.Module,
)

// In a handler / middleware:
ok, err := enforcer.Enforce(userID, r.URL.Path, r.Method)
```

## Policy CSV format (RBAC)

```csv
p, alice, /admin, GET
p, alice, /data,  GET
p, alice, /data,  POST
g, bob, alice      // bob inherits alice's roles
```

## Models

| Constant | Use case |
|---|---|
| `authz.ModelRBAC` | Role-based, allow-only |
| `authz.ModelRBACWithDeny` | Role-based with explicit deny |

Custom model DSL strings accepted via `Options.Model`.

## Adapters

`authz.NewFileAdapter(path)` for files. For Postgres use
`github.com/casbin/casbin-pg-adapter`. For Redis use
`github.com/casbin/redis-adapter/v3`.

## Don't

- Don't skip scope/role checks in hot paths — Enforce is O(policy size).
  Cache the result in `cache/memory` if needed.
- Don't put business logic in the policy model DSL — keep it to access
  control only.
