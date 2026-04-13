# Agent guide — leader/

Single-leader election with pluggable backends.

| Subpackage | Backend | Pick when |
|---|---|---|
| `leader/k8s` | Kubernetes Lease (client-go) | Running on k8s; avoids adding pg dep to k8s-only apps |
| `leader/pg`  | PostgreSQL advisory lock     | Anywhere else (Docker Compose, Swarm, Nomad, bare Linux, k8s without Lease RBAC). Needs a *pgxpool.Pool |

## Conventions

- Apps pick ONE backend. Wiring both is a config error (two electors
  fighting for one task).
- `leader.Callbacks` is shared across backends — swap backends without
  changing handler code.
- `OnStartedLeading(ctx)` handler MUST return promptly on ctx cancel
  or risk concurrent leaders across replicas.

## Trade-offs

| Concern | `leader/k8s` | `leader/pg` |
|---|---|---|
| External dep | k8s API + Lease RBAC | pg connection |
| Failover speed | ~LeaseDuration (15s default) | TCP keepalive + advisory-lock retry (~2s) |
| Setup | RBAC rolebinding | pg_try_advisory_lock — no schema |
| Crash safety | Lease expires after TTL | Session dies → lock released |

## Don't

- Don't tune `leader/k8s.Lease.Duration` below 2 × `Renew` — elector
  refuses the config.
- Don't share a `leader.name` (pg backend) across unrelated electors;
  names hash to int8 keys, collision = contention.
