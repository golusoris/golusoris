# casbin/casbin/v2 — v2.105.0 snapshot

Pinned: **v2.105.0**
Source: https://pkg.go.dev/github.com/casbin/casbin/v2@v2.105.0

## Core concepts

- **Model** — defines the PERM meta-model (Policy, Effect, Request, Matchers)
- **Policy** — rules stored in adapter (CSV file, DB, Redis, etc.)
- **Enforcer** — evaluates `(subject, object, action)` against the model+policy

## Usage

```go
import "github.com/casbin/casbin/v2"

// Load from model + policy files
e, err := casbin.NewEnforcer("model.conf", "policy.csv")

// Load from model string + adapter
e, err := casbin.NewEnforcer(m, adapter)

// Check permission
ok, err := e.Enforce("alice", "/data/1", "read")

// Batch check
results, err := e.BatchEnforce([][]interface{}{
    {"alice", "/data/1", "read"},
    {"bob", "/data/2", "write"},
})
```

## RBAC model (common)

```ini
# model.conf
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
```

## Policy management

```go
e.AddPolicy("alice", "/data/1", "read")
e.RemovePolicy("alice", "/data/1", "read")
e.AddRoleForUser("alice", "admin")
e.GetRolesForUser("alice")
e.GetUsersForRole("admin")
e.DeleteRoleForUser("alice", "admin")
```

## Adapters

```go
// PostgreSQL adapter
import gormadapter "github.com/casbin/gorm-adapter/v3"
a, _ := gormadapter.NewAdapter("postgres", dsn, true)
e, _ := casbin.NewEnforcer("model.conf", a)

// Auto-save on policy change
e.EnableAutoSave(true)
e.SavePolicy()
```

## golusoris usage

- `authz/` — `*casbin.Enforcer` provided via fx; chi middleware wraps `Enforce`.

## Links

- Docs: https://casbin.org/docs/overview
- Changelog: https://github.com/casbin/casbin/blob/master/CHANGELOG.md
