<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — leader/pg/

Single-leader election via PostgreSQL session-scoped advisory lock
(`pg_try_advisory_lock`). Works anywhere app already has pg pool — Compose,
Swarm, Nomad, bare Linux, or k8s without Lease API. One of two `leader/`
backends — apps pick exactly one (see also `leader/k8s`).

## Wiring

```go
fx.New(
    golusoris.Core,
    golusoris.DB,
    // requires *pgxpool.Pool + clock.Clock in the graph
    pg.Module("outbox-drainer", leader.Callbacks{
        OnStartedLeading: drainOutbox, // ctx canceled on stop — return promptly
    }),
)
```

`Module(cb)` **Provides** loaded `Options`; **Requires** `*pgxpool.Pool`,
`clock.Clock`, `*slog.Logger`, `fx.Lifecycle`. Election runs in goroutine on
`OnStart`; `OnStop` cancels it and closes dedicated connection, releasing lock. `Run(ctx, pool, opts, clk, cb)` is callable directly.

## Config

Keys under `leader` prefix (env `APP_LEADER_*`). `leader.enabled=false`
(default) skips wiring entirely.

```yaml
leader:
  enabled: true
  name: outbox-drainer   # human name, FNV-64a-hashed into the int64 lock key — required
  identity: ""           # default: hostname → "unknown"
  pg:
    retry: 2s            # acquisition retry period when the lock is held
```

## Notes

- Dedicates one pooled connection for lock's whole lifetime — size pool
 accordingly. lock auto-releases on session end (graceful close **or**
 crash via TCP keepalive), so there's no TTL/renewal tuning.
- `name` must be unique per elector: two electors with same name hash to  same key and contend (caller config error, not library bug).
- Empty `leader.name` with `enabled=true` fails construction.
