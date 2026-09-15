<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0019: Adopt praetor governance and publish a machine-readable capability contract

- **Status**: Accepted
- **Date**: 2026-09-14
- **Deciders**: @lusoris
- **Tags**: governance, agents, tooling, fleet

## Context

Every Go repository in the fleet is meant to deduplicate onto golusoris, and
[cordanallm/praetor](https://github.com/cordanallm/praetor) is the engine that
measures and drives that: `standardsctl needs scan` turns a repository's
`go.mod` + imports into a `.needs.yaml` demand manifest, `needs aggregate`
rolls the fleet up, and `needs migrate` rewrites imports. Until now praetor
had to *guess* what the framework provides from directory names and a
hand-maintained catalog, which reported 20.9 % fleet coverage with
`custom.v2`-style noise. In the other direction, golusoris had no HISS-16
baseline, no compiled vendor agent context, and a hand-written `CLAUDE.md`
that praetor's transpiler would have overwritten.

## Decision

1. **Publish the contract.** Repo-root [`capabilities.yaml`](https://github.com/golusoris/golusoris/blob/main/capabilities.yaml)
   (schema: [`core/capabilities`](../../core/capabilities/)) lists every
   importable package, its Go module, the capability keys it satisfies (the
   praetor taxonomy: `db.postgres`, `cache.redis`, `mcp.server`, …) and the
   third-party modules it `replaces`. `capabilities_test.go` fails when the
   tree and the file drift. Praetor reads this file first and falls back to
   heuristics only when it is absent.
2. **Adopt praetor governance in full** (`standardsctl adopt`): `.standards.yaml`
   (archetype `framework`, facets `security:high`, `api:public-contract`,
   `docs:seo-portal`, `agent:sandboxed`), `.standards.lock`, a HISS-16 debt
   baseline (1 807 legacy infractions, ratcheted down over time), compiled
   vendor context for Claude / Cursor / Copilot / Windsurf / Gemini / Codex,
   IDE configs, devcontainer, label taxonomy and branch ruleset.
3. **`AGENTS.md` is the single canonical harness.** The former `CLAUDE.md`
   body lives in its `## Claude Code` section; the transpiler (fixed upstream
   to carry per-vendor sections) compiles `CLAUDE.md` from it. Editing
   `CLAUDE.md` directly is now a pre-commit failure.
4. **`make verify-all`** is the universal gate: build + lint + sec + tests for
   root and core, the capabilities drift guard, `compile-context --verify`,
   `standardsctl audit`, `reuse lint`.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Keep the catalog inside praetor | No framework change | Rots with every release; already wrong for 10 packages | Contract must live with the code it describes |
| Contract-only adoption (no HISS baseline / IDE files) | Smaller footprint | Fleet policy is "one harness everywhere"; partial adoption keeps two conventions alive | Full adoption (user decision) |
| Generate `capabilities.yaml` at build time | Never drifts | Consumers need the file at rest (praetor reads a checkout) | Checked-in file + drift test |

## Consequences

- **Positive**: fleet coverage measured against the real framework: 97.5 %,
  with the remaining gaps (`otelpgx`, `raft`, `anthropic-sdk-go`, `decimal`,
  `embedded-postgres`, `req`, `hclog`, `ginkgo/gomega`, `goptuna`,
  `parquet-go`, `containerd/nri`, `go.uber.org/mock`, `goleak`) an explicit
  backlog in `.workingdir/FLEET-DEMAND.md`. praetor's own `.needs.yaml` is at
  100 %.
- **Negative**: praetor-managed files (`.zed/`, `.helix/`, `lua/`, …) live in
  the tree; the HISS scanner's function-length rule (60 LOC) is stricter than
  the framework's `funlen` (120) — new code must satisfy both.
- **Neutral / follow-ups**: adding a package now means adding its
  `capabilities.yaml` entry; praetor's own code migration onto `core/*` is
  tracked in its `docs/golusoris-migration-plan.md`.

## References

- [ADR-0017](0017-lean-core-submodule.md) · [`core/capabilities/AGENTS.md`](https://github.com/golusoris/golusoris/blob/main/core/capabilities/AGENTS.md)
- praetor: `internal/needs/framework.go` (`capabilities.yaml` reader), `internal/compiler` (vendor sections)
- HISS-16 specification: <https://standards.cordana.ai/standards/hiss-16/>
