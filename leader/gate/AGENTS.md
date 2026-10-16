<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: EUPL-1.2
-->

<!-- Ported from SnatchArr api/internal/leaderx at 6111de5a. -->

# Agent guide — leader/gate/

Loop-lifecycle gate starting and stopping periodic jobs across leadership transitions.

## Wiring

```go
fx.New(
    golusoris.Core,
    gate.Module,
    fx.Provide(
        NewJob,
        gate.Provide[*Job](), // registers into fx group leader_loops
    ),
    fx.Invoke(func(reg *statuspage.Registry, g *gate.Gate) {
        reg.Register(g.Check("leader"))
    }),
)
```

`Module` **Provides** `*Gate`, loaded `Options`; **Requires** `clock.Clock`, `*slog.Logger`, `fx.Lifecycle`. `*pgxpool.Pool` required only when election enabled without custom `Elect`.

## Loops

Register loop types implementing `gate.Loop` (`Start(ctx)`, `Stop(ctx)`). `gate.Periodic` provides timer-driven loop calling tick under deadline.

## Config

Prefix `leader` (`APP_LEADER_*`):

```yaml
leader:
  enabled: false # false: loops run with process; true: elector controls loops
  name: loop-runner # lock name when using pg elector
  pg:
    retry: 2s # pause between failed elections
```

## Health

`Gate.Check(name)` registers readiness check:
- Down before `Run` starts or after `Run` returns (`ErrNotRunning`).
- Down when loop start fails in current term.
- Down while election failing until retry succeeds.
- Up when idling for leadership.

## Never

- Singleton loop started from own module hooks: register via `gate.Provide`.
- `gate.Module` plus `leader/pg.Module`: gate runs own election; pick one.
- Blocking `Stop`: bounded by `gate.StopTimeout` (30s).
