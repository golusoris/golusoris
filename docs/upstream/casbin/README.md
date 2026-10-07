<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# casbin/casbin/v3 — v3.11.0 snapshot

Pinned: **v3.11.0**
Source: [tagged source](https://github.com/casbin/casbin/tree/v3.11.0)

## Core concepts

- **Model** — defines the PERM meta-model (Policy, Effect, Request, Matchers)
- **Policy** — rules stored in adapter (CSV file, DB, Redis, etc.)
- **Enforcer** — evaluates `(subject, object, action)` against the model+policy

## Usage

```go
import "github.com/casbin/casbin/v3"

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
added, err := e.AddPolicy("alice", "/data/1", "read")
removed, err := e.RemovePolicy("alice", "/data/1", "read")
roleAdded, err := e.AddRoleForUser("alice", "admin")
roles, err := e.GetRolesForUser("alice")
users, err := e.GetUsersForRole("admin")
roleRemoved, err := e.DeleteRoleForUser("alice", "admin")
```

Every management call returns an error. Mutation calls also return whether the
policy changed; do not discard either result.

## Adapters

Golusoris accepts Casbin's `persist.Adapter` contract. The selected v3 module
includes `persist/file-adapter`; database adapters are separate dependencies
and must be pinned and audited before use. `EnableAutoSave(true)` persists
supported policy mutations through the configured adapter; handle
`SavePolicy()` errors when forcing a full save.

## golusoris usage

- `authz/` — `*casbin.Enforcer` provided via fx; chi middleware wraps `Enforce`.

## Links

- [Documentation](https://casbin.org/docs/overview)
- [Tagged source](https://github.com/casbin/casbin/tree/v3.11.0)
