<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — leader/always/

Always-leader backend for standalone single-replica runs. No lock, no
Lease: replica leads from fx start to stop, so singleton tasks written
against `leader.Callbacks` run unchanged without k8s or pg.

## Wiring

```go
fx.New(
    golusoris.Core,
    always.Module(leader.Callbacks{OnStartedLeading: runScheduler}),
    always.NamedModule("gc", leader.Callbacks{OnStartedLeading: runGC}),
)
```

Swap for `leader/k8s` or `leader/pg` in clustered builds; callbacks and
config keys stay same.

## Config

`leader.enabled` (false), `leader.identity` (hostname). Named:
`leader.elections.<key>.{enabled,identity}`.

## Behaviour

- Each callback once per run: `OnNewLeader(identity)`,
 `OnStartedLeading(ctx)`, then on stop ctx canceled + `OnStoppedLeading`.
- `Run(ctx, opts, cb)` blocks until ctx canceled; returns nil.

## Don't

- Don't deploy with >1 replica: every replica leads.
- Don't leave `leader.enabled=false` expecting leadership: disabled
 backend never fires callbacks (same as other backends).
