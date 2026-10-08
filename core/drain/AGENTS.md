<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/drain/

Seam between servers and readiness drain. Server wraps its stop hook with optional `drain.Gate`; shutdown waits for drain window, server never imports readiness owner.

## Key surface

| Symbol | Purpose |
| --- | --- |
| `drain.Gate` | `Wrap(fx.Hook) fx.Hook`; defers OnStop until readiness drained |
| `drain.Wrap(g, hook)` | nil-safe: nil gate -> hook unchanged |

## Wiring

- `k8s/health.Module` provides `drain.Gate` (its `*ShutdownGate`).
- `httpx/server`, `grpc`: take `drain.Gate` as optional fx param, append `drain.Wrap(p.Gate, hook)`.
- App-owned server: same, `lc.Append(drain.Wrap(gate, fx.Hook{...}))`.

## Don't

- Don't import `k8s/health` from a server package for gating; keeps `bootstrap` import graph lean (`bootstrap` test).
