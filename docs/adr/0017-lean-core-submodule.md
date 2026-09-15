<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0017: `core/` is a separate, lean Go module

- **Status**: Accepted
- **Date**: 2026-09-14
- **Deciders**: @lusoris
- **Tags**: modules, dependencies, governance, breaking-change

## Context

The framework's root module carries ~390 `require` lines: every opt-in
integration (k8s, temporal, kafka, stripe, ebpf, …) is a dependency of the
module that also holds `config`, `log`, and `clikit`. That is fine for apps
that want the whole toolbox, but it disqualified golusoris for the other class
of fleet consumer — governance and agent tooling such as
[cordanallm/praetor](https://github.com/cordanallm/praetor), which is a
one-dependency CLI by design (`security:high`, SLSA L3, SBOM-audited). Pulling
the root module into praetor to reuse `config` would have dragged the whole
graph into its `go.sum` and SBOM.

Go module-graph pruning limits what gets *built*, not what a consumer must
*trust*. The only way to give small consumers a small trust surface is a
separate module.

## Decision

We carve `github.com/golusoris/golusoris/core` as its own Go module holding
the packages every consumer needs and nothing that drags a heavy graph:

`config` · `codec/yaml` · `log` · `clock` · `errors` · `crypto` ·
`crypto/receipt` · `id` · `validate` · `version` · `clikit` · `mcp` · `gitx` ·
`gitx/worktree` · `astx` · `capabilities`.

Import paths move to `github.com/golusoris/golusoris/core/<pkg>`. The root
module requires `core` (with an in-repo `replace` for development, the same
pattern the 19 heavy sub-modules already use); consumers of the root module
get `core` transitively. `clikit/tui` stays in the root module so bubbletea
stays out of core.

Release procedure: tag `core/vX.Y.Z` and `vX.Y.Z` on the same commit
(release-please: `core` component, `tag-separator: "/"`).

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Keep one root module | No import-path change | Praetor and every small CLI inherit the full graph in `go.sum`/SBOM | Defeats the fleet-dedupe goal for the tooling class |
| Invert: root = kernel, every other area its own module | Import paths unchanged | ~55 new modules, per-module tags/apidiff/renovate churn | Operational cost far above the benefit |
| `internal/`-style vendoring in praetor | Nothing to publish | Duplicates the code we are trying to deduplicate | Contradicts the initiative |

## Consequences

- **Positive**: a consumer that imports only `core/*` sees ~20 direct deps
  (koanf, fx, cobra, clockwork, argon2id, validator, go-sdk, x/mod, yaml/v3).
  praetor reaches 100 % readiness against the capability contract.
- **Negative**: every existing import of the ten moved packages breaks
  (v0.9.0, `Migration:` footer). `astx.RewriteImports` ships the codemod;
  `docs/migrations/v0.9.0.md` lists the mapping.
- **Neutral / follow-ups**: CI runs lint/gosec/govulncheck/test/build for
  both modules; coverage is merged. `capabilities.yaml` records the module of
  every package so tooling can add the right `require`.

## References

- [`core/AGENTS.md`](https://github.com/golusoris/golusoris/blob/main/core/AGENTS.md) · [`capabilities.yaml`](https://github.com/golusoris/golusoris/blob/main/capabilities.yaml)
- [ADR-0019](0019-praetor-governance-and-capability-contract.md)
- OpenTelemetry-Go layout (`sdk/`, `exporters/*` as sub-modules) — the same shape.
