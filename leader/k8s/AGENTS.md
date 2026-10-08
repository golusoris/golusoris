<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — leader/k8s/

Single-leader election via Kubernetes Lease API (client-go
`leaderelection`). One pod wins Lease and runs leader callback; on
loss/restart another pod takes over. One of two `leader/` backends — apps pick
exactly one (see also `leader/pg`).

## Wiring

```go
fx.New(
    golusoris.Core,
    // requires *rest.Config in the graph (from k8s/client)
    k8s.Module("outbox-drainer", leader.Callbacks{
        OnStartedLeading: drainOutbox, // ctx canceled on lease loss — return promptly
    }),
)
```

`Module(cb)` **Provides** loaded `Options`; **Requires** `*rest.Config`,
`*slog.Logger`, `fx.Lifecycle`. Election runs in goroutine started on `OnStart`
and stopped (lease released, `ReleaseOnCancel`) on `OnStop`. `Run(ctx, k, opts, cb)`
is also callable directly with `kubernetes.Interface`.

## Config

Keys under `leader` prefix (env `APP_LEADER_*`). `leader.enabled=false`
(default) skips wiring entirely.

```yaml
leader:
  enabled: true
  namespace: default        # Lease namespace
  name: outbox-drainer      # Lease name — required when enabled
  identity: ""              # default: $POD_NAME → hostname → "unknown"
  lease:
    duration: 15s           # Lease TTL    (client-go controller defaults)
    renew:    10s           # renew deadline
    retry:    2s            # retry period when not leader
```

## Notes

- Requires RBAC for `coordination.k8s.io` Leases (get/create/update) in  namespace; only runs meaningfully inside cluster (needs `*rest.Config`).
- `OnStartedLeading`'s ctx is canceled on lease loss — handler code must exit on
 cancellation or risk concurrent leaders.
- Empty `leader.name` with `enabled=true` fails construction.
