<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — leader/

Single-leader election with pluggable backends.

| Subpackage | Backend | Pick when |
| --- | --- | --- |
| `leader/k8s` | Kubernetes Lease (client-go) | Running on k8s; avoids adding pg dep to k8s-only apps |
| `leader/pg` | PostgreSQL advisory lock | Anywhere else (Docker Compose, Swarm, Nomad, bare Linux, k8s without Lease RBAC). Needs a *pgxpool.Pool |
| `leader/always` | none: always leader | Standalone single replica (desktop, dev, one-node install). Never with >1 replica |

## Conventions

- Apps pick ONE backend. Wiring both is config error (two electors
 fighting for one task).
- `leader.Callbacks` is shared across backends — swap backends without
 changing handler code.
- `OnStartedLeading(ctx)` handler MUST return promptly on ctx cancel
 or risk concurrent leaders across replicas.
- Several singleton tasks -> one `NamedModule(key, cb)` per task (any
 backend, beside `Module` or not). Options under
 `leader.elections.<key>` (same keys as `leader.*`), env
 `APP_LEADER_ELECTIONS_<KEY>_*`. key `[a-z][a-z0-9]{0,62}`; each needs
 own `name` (Lease / lock key).
- State: `leader.Status.IsLeader()`. NamedModule provides `*leader.Status`
 tagged `name:"<key>"`. `Module` provides none (two backends wired, one
 disabled, stays legal): wrap callbacks yourself,
 `st := leader.NewStatus(); k8s.Module(st.Observe(cb))`.
- Status true from `OnStartedLeading` until term ctx ends or
 `OnStoppedLeading`; term id stops late cancel clearing newer term.

## Trade-offs

| Concern | `leader/k8s` | `leader/pg` |
| --- | --- | --- |
| External dep | k8s API + Lease RBAC | pg connection |
| Failover speed | ~LeaseDuration (15s default) | TCP keepalive + advisory-lock retry (~2s) |
| Setup | RBAC rolebinding | pg_try_advisory_lock — no schema |
| Crash safety | Lease expires after TTL | Session dies → lock released |

## Don't

- Don't tune `leader/k8s.Lease.Duration` below 2 × `Renew` — elector
 refuses config.
- Don't share `leader.name` (pg backend) across unrelated electors;
 names hash to int8 keys, collision = contention.
