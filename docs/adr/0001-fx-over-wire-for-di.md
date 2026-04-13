# ADR-0001: fx over wire for dependency injection

- **Status**: Accepted
- **Date**: 2026-04-13 (backfill — decision predates ADR practice)
- **Deciders**: @lusoris
- **Tags**: core, di, lifecycle

## Context

Every framework module (config, log, db, http, jobs, …) needs to be composable so apps pull only what they need. Two mainstream Go DI tools:

- **[google/wire](https://github.com/google/wire)** — compile-time code generation. Static, fast, no runtime overhead.
- **[uber-go/fx](https://github.com/uber-go/fx)** — runtime DI with explicit lifecycle (`OnStart` / `OnStop`) hooks.

The framework also needs ordered startup/shutdown (DB before HTTP, OTel before everything emitting telemetry, leader election before the outbox drainer). Per [PLAN.md §2.1](../../.workingdir/PLAN.md), no `init()` side effects are allowed — all wiring must be explicit.

## Decision

We will use `go.uber.org/fx` as the dependency-injection and lifecycle backbone for every package in the framework. Every module exposes its capability as `fx.Module` or `fx.Options`; apps compose modules in `fx.New(...).Run()`.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| `google/wire` | Compile-time, zero runtime cost, errors caught early | No lifecycle primitives — start/stop ordering is the app author's problem; codegen step on every change | Lifecycle is the killer feature for our use case (DB → leader → drainer ordering). Manual ordering across 20+ apps doesn't scale. |
| Hand-rolled `func New…(deps…) (*T, error)` | No deps, full control | Every app re-implements wiring + lifecycle ordering. Defeats the framework's purpose. | Reinvents the wheel. |
| `uber-go/dig` (fx's underlying container) | Runtime DI, lighter than fx | No lifecycle, no module composition — fx adds exactly what we need on top | fx already wraps dig with the missing pieces. |

## Consequences

- **Positive**: Apps wire 5-line `fx.New(golusoris.Core, golusoris.DB, golusoris.HTTP, ...)` instead of 100 lines of glue. Lifecycle hooks make ordered shutdown trivial. Each module is independently testable by composing only the deps it needs.
- **Negative**: Runtime errors when graphs are misconfigured (missing provider, type collision) instead of compile-time errors. Mitigated by `go test` + `fxtest.New(t, ...)` in every module's tests.
- **Follow-ups**: Every new module ships with an `fx.Module` + a smoke test (`fxtest.New`) verifying it can start + stop in isolation. Documented in [.claude/skills/wire-fx-module/](../../.claude/skills/wire-fx-module/).

## References

- fx pinned at v1.24.0 — see [`docs/upstream/fx/`](../upstream/fx/).
- [PLAN.md §2.1](../../.workingdir/PLAN.md) — Power of 10 rule on no `init()` side effects.
