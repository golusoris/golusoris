<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0024: Keep flags provider contract

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: @lusoris
- **Tags**: `flags`, `dependencies`

## Context

The package `flags` provides typed feature-flag evaluation backed by a pluggable `Provider` contract ([`flags/flags.go:5-22`](https://github.com/golusoris/golusoris/blob/main/flags/flags.go#L5-L22)). The package intentionally mirrors the OpenFeature evaluation model so that migrating to the official OpenFeature Go SDK remains straightforward if external backends are required. The current implementation consists of 195 physical lines of Go (119 non-blank, non-comment lines) backed by 222 lines of unit tests across [`flags/*_test.go`](https://github.com/golusoris/golusoris/tree/main/flags) (`flags/flags_test.go:1-138`, `flags/module_test.go:1-84`).

Key surface and invariants re-verified against [`flags/flags.go`](https://github.com/golusoris/golusoris/blob/main/flags/flags.go) and [`flags/module.go`](https://github.com/golusoris/golusoris/blob/main/flags/module.go):

- **Core types and contracts**: `EvalContext` targeting map (`map[string]any`, [`flags/flags.go:34`](https://github.com/golusoris/golusoris/blob/main/flags/flags.go#L34)), `Provider` interface with `Metadata() ProviderMetadata` and generic `Evaluate(ctx, key, defaultValue, evalCtx) (any, error)` (:38-44), `ProviderMetadata{Name string}` (:47-49), and `Client` (:52-54).
- **Constructors and evaluation**: `New(Provider) *Client` (:57-62) defaulting to `NoopProvider` on nil. Typed client evaluation methods `Bool` (:66), `String` (:79), `Int` (:92), and `Float` (:110) with optional variadic `...EvalContext` (:127) and value-only returns that fall back gracefully to the caller default on error or missing keys.
- **Provider implementations**: In-process `MemoryProvider` (:138-176) with `Set` (:149), `Delete` (:156), and `NewMemoryProvider` (:144) for local development and unit tests; `NoopProvider` (:182-190) returning caller defaults as a safe null object. Error constructor `ErrUnknownFlag` (:195) for strict-mode callers.
- **Dependency injection**: `flags.Module` ([`flags/module.go:31-36`](https://github.com/golusoris/golusoris/blob/main/flags/module.go#L31-L36)) exposing `Options` with `koanf:"backend"` (:39-43) to select `"noop"` (default) or `"memory"`, surfaced via `golusoris.Flags` ([`golusoris.go:198`](https://github.com/golusoris/golusoris/blob/main/golusoris.go#L198)).
- **Implementation inventory**: Only `MemoryProvider` and `NoopProvider` exist in the repository. Commentary in `flags/flags.go:7-8` mentioning Postgres, Redis, or LaunchDarkly describes potential deployment options, not implemented code. No external database or SaaS flag vendor integrations currently exist.

Issue #1178 evaluated whether to adopt the official OpenFeature Go SDK (`github.com/open-feature/go-sdk`) to replace the in-tree mirror.

## Considered options

| Option | Licence | Latest Tag / Date | Maintenance Status | Transitive Deps | Evaluation |
| :--- | :--- | :--- | :--- | :--- | :--- |
| a) Replace with OpenFeature Go SDK (`github.com/open-feature/go-sdk`) | Apache-2.0 | v1.20.0 (2026-10-24) | Active; 0 advisories (GitHub, OSV, 2026-10-28) | 1 direct module / 0 new transitive paths (601 → 602 unique module paths) | Rejects: breaks public `flags.Provider` ABI ([HISS-14](https://github.com/golusoris/golusoris/blob/main/AGENTS.md#L37)); replaces 2-method `Provider` with 7-method `FeatureProvider`; package `init()` singleton state (`openfeature.go:16`) and global `slog.Info` in event internals (`event_executor.go:302`, `:383`) violate repository rules against `init()` side effects ([`AGENTS.md:126`](https://github.com/golusoris/golusoris/blob/main/AGENTS.md#L126)) and un-scoped global loggers ([`AGENTS.md:129`](https://github.com/golusoris/golusoris/blob/main/AGENTS.md#L129)). |
| b) Bi-directional adapter shims (additive) | Apache-2.0 / EUPL-1.2 | v1.20.0 (2026-10-24) | Active | 1 direct module / 0 new transitive paths | Deferred: preserves append-only ABI (HISS-14) by adapting `flags.Provider` $\leftrightarrow$ `FeatureProvider`; wires isolated SDK instances into `fx` lifecycle hooks (`OnStart`/`OnStop`); preferred adoption path if external contrib backends are needed, but unnecessary overhead while only in-memory and noop providers are used. |
| c) Keep in-tree mirror contract | EUPL-1.2 | In-tree (`flags`) | Active (maintainer) | 0 external modules | Chosen: 195 LOC + 222 LOC tests; zero external dependencies; strict compliance with zero-`init()` and scoped logging invariants; fully satisfies current application needs. |

Adopting third-party OpenFeature Go SDK v1.20.0 directly (Option a) presents architectural policy and compatibility barriers:

- **Public ABI break**: Upstream [`FeatureProvider`](https://github.com/open-feature/go-sdk/blob/v1.20.0/openfeature/provider.go#L50) mandates 7 methods: `Metadata() Metadata`, `BooleanEvaluation(...) BoolResolutionDetail`, `StringEvaluation(...) StringResolutionDetail`, `FloatEvaluation(...) FloatResolutionDetail`, `IntEvaluation(...) IntResolutionDetail`, `ObjectEvaluation(...) InterfaceResolutionDetail`, and `Hooks() []Hook`. Replacing `flags.Provider` (2 methods: `Metadata() ProviderMetadata`, `Evaluate(...) (any, error)`) breaks all existing provider implementations and constructor call sites, violating [HISS-14](https://github.com/golusoris/golusoris/blob/main/AGENTS.md#L37) append-only public contract invariants.
- **Package `init()` singleton initialization**: `github.com/open-feature/go-sdk/openfeature/openfeature.go:16` executes package `init()` on import, creating a global singleton API and event executor (`event_executor.go:33`). This contradicts the repository directive forbidding `init()` side effects ([`AGENTS.md:126`](https://github.com/golusoris/golusoris/blob/main/AGENTS.md#L126)).
- **Un-scoped global logging**: Event executor internals call package-level `slog.Info` directly (`event_executor.go:302`, `:383`), violating the requirement that logging use injected `*slog.Logger` handlers with zero global logger dependencies ([`AGENTS.md:129`](https://github.com/golusoris/golusoris/blob/main/AGENTS.md#L129)). While `isolated.NewAPI()` exists (v1.18.0+), importing the package still runs the singleton `init()`, and isolated API instances lack a constructor-injected logger field (`openfeature_api.go:111`), continuing to use process-wide provider bindings (`:21`, `:31`).
- **Dependency footprint**: Upstream SDK v1.20.0 (tag commit `dc47c918c95fd22018a60ac2c9469e67454a6187`, module checksum `h1:cQV15LaPAQi1bLEoR2KHlzVxm6UuEqrohJ7qXVq+ZAk=`, Apache-2.0, zero security advisories across GitHub and OSV checked 2026-10-28) adds 1 direct module path (`github.com/open-feature/go-sdk`) and 0 new transitive module paths (601 → 602 unique module paths in `go.sum`). Although the dependency cost is modest, adopting the SDK solely for in-memory and noop null-object evaluations yields zero feature benefit while creating policy conflicts.

Option b (bi-directional adapter shims) avoids breaking HISS-14 by maintaining `flags.Provider` and `flags.Client` intact while providing adapters to wrap `flags.Provider` as `FeatureProvider` and SDK `*openfeature.Client` as `flags.Provider`. It manages SDK instances via `fx.Lifecycle` (`OnStart` for `api.SetProviderAndWait`, `OnStop` for `api.Shutdown`). While Option b is architecturally sound and represents the designated migration path if third-party providers are needed, introducing adapter machinery in both directions is unjustified without a concrete backend consumer.

## Decision

Keep the in-tree flags provider contract in `flags` (Option c). Decline adopting `github.com/open-feature/go-sdk` at this time.

The current 195 LOC implementation fulfills all existing application requirements (local development testing and safe null-object initialization) with zero external dependencies, zero ABI breakage, zero `init()` side effects, and scoped logging. If external backends from the OpenFeature contrib ecosystem become necessary in the future, adoption will follow Option b.

## Consequences

- **Positive**: Zero external dependency additions across the module tree (maintains baseline of 601 unique module paths).
- **Positive**: Preserves the public `flags.Provider` and `flags.Client` ABI without breaking changes (HISS-14).
- **Positive**: Strict adherence to zero-`init()` side effects and scoped `slog.Logger` injection rules.
- **Negative**: No direct out-of-the-box integration with OpenFeature contrib ecosystem providers (such as `flagd` or `go-feature-flag`).
- **Revisit triggers**: Re-evaluate and implement Option b (bi-directional adapter shims) when:
  1. A consumer requires a production backend from the OpenFeature contrib ecosystem (e.g. `github.com/open-feature/go-sdk-contrib/providers/flagd` v0.8.0, `github.com/open-feature/go-sdk-contrib/providers/go-feature-flag` v1.1.3); or
  2. Upstream OpenFeature Go SDK removes package `init()` singleton state initialization and un-scoped `slog.Info` calls, enabling clean instance isolation and injected logging.

## References

- Issue #1178 (Evaluate OpenFeature SDK adoption for flags package).
- OpenFeature Go SDK: [`github.com/open-feature/go-sdk`](https://github.com/open-feature/go-sdk) (v1.20.0, commit `dc47c918c95fd22018a60ac2c9469e67454a6187`).
- OpenFeature Go SDK Contrib: [`github.com/open-feature/go-sdk-contrib`](https://github.com/open-feature/go-sdk-contrib) (tree `8e40e4c5f33dd33b530dad747f8e7417f7c01ca6`).
- OpenFeature Specification: [https://openfeature.dev/specification/](https://openfeature.dev/specification/).
- ADR-0023: Keep hand-written OAuth2 authorization server (issue #1175).
- Golusoris Coding Principles: [`docs/principles.md`](../principles.md).
- Golusoris Agent Operating Harness: [`AGENTS.md`](https://github.com/golusoris/golusoris/blob/main/AGENTS.md).
