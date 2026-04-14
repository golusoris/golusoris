# ADR-0006: Pluggable leader election (k8s Lease + pg advisory)

- **Status**: Accepted
- **Date**: 2026-04-13 (backfill — Step 6.5b refactor)
- **Deciders**: @lusoris
- **Tags**: leader, runtime, k8s

## Context

Several framework features must run on exactly one replica at a time: outbox drainer, cron scheduler, periodic cache refresh. Original implementation (`k8s/leader/`) bound the framework to Kubernetes' Lease resource — fine for k8s but blocks every non-k8s deployment target listed in [principles.md §2.9](../principles.md): Docker Compose, Swarm, Nomad, systemd, bare Linux.

Two viable backends emerged:

- **client-go Lease** (`coordination.k8s.io/v1`) — TTL-based, native to k8s, requires the Lease API.
- **PostgreSQL session-scoped advisory locks** (`pg_try_advisory_lock`) — held by an open connection, released automatically on session end (graceful or crash).

## Decision

We will promote `leader/` to a top-level package with two backend sub-modules:

- `leader/k8s` — client-go Lease (the original implementation, moved).
- `leader/pg` — `pg_try_advisory_lock` over a dedicated `*pgxpool.Pool` connection.

Both backends share a `leader.Callbacks` struct (`OnNewLeader`, `OnStartedLeading`, `OnStoppedLeading`) so consumers don't bind to either backend's wire types.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Keep `k8s/leader` as the only backend | Simpler API surface | Locks framework into k8s | Kills the Docker Compose / systemd story. |
| etcd / Consul / Zookeeper backend | Battle-tested at scale | Adds another infra dep apps don't already have | Apps already have Postgres; etcd is gratuitous. |
| Redis Redlock | Familiar to many | Algorithm has known [safety concerns](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html); needs Redis cluster for HA | Pg + k8s give us strictly better safety. |
| Single-replica deployment (no election) | No code | App scaling becomes manual | Defeats horizontal scaling story. |

## Consequences

- **Positive**: Apps pick a backend per deployment target. k8s apps wire `leader/k8s`, single-region apps on Hetzner wire `leader/pg`, multi-cluster apps can mix per environment. Pg backend is fail-safe on crash (TCP keepalive detects dead session, lock auto-releases) — no TTL tuning. Real-pg integration test proves two-replicas-one-leader.
- **Negative**: Two backends to maintain. Apps must pick one (no "auto-detect" — surface area too noisy). Pg backend dedicates one connection from the pool for the lock duration.
- **Follow-ups**: When a third backend is requested (etcd? cloud-native KV?), the `leader.Callbacks` interface is the contract — backend-specific timing knobs live in `BackendOptions`. Document on the per-backend AGENTS.md when added.

## References

- [`leader/pg/pg.go`](../../leader/pg/pg.go) — pg backend.
- [`leader/k8s/`](../../leader/k8s/) — k8s backend.
