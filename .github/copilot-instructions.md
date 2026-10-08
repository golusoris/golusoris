<!-- markdownlint-disable MD013 -->
<!-- Compiled automatically by praetorctl compile-context from AGENTS.md. DO NOT EDIT DIRECTLY. -->

<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

<!-- markdownlint-disable MD013 MD025 -->
# golusoris Agent Operating Harness

Before concluding any turn:

```bash
make verify-all
```

`make verify-all` runs `praetorctl audit`, context verification, and repository tests. Exit 0 means verified. On failure, agents keep full logs in ephemeral storage and report at most 1,500 diagnostic tokens.

## Core Directives & Invariants — Golusoris HISS-21 Lattice

| Invariant | Scope | NASA Rule | Rule | Gate in this repository |
| :--- | :--- | :--- | :--- | :--- |
| **HISS-01** | Control Flow | Rule 1 | Recursion strictly prohibited; call graph must be a DAG; zero `goto`. | `praetorctl audit` HISS scan: direct plus mutual recursion and `goto` |
| **HISS-02** | Loops & I/O | Rule 2 | Scalar upper bound on all loops; explicit `context.Context` timeout on all I/O. | `praetorctl audit` loop scan; all-module lint; required full-tree Semgrep HTTP rules |
| **HISS-03** | Memory | Rule 3 | No steady-state heap churn in hot paths; preallocate and pool instead. | `scripts/ci/allocation-budget.sh` fails on byte or allocation regressions in named `core/id`, `core/crypto`, and `cache/memory` benchmarks; other hot paths remain review-enforced |
| **HISS-04** | Complexity | Rule 4 | Cyclomatic $\le 10$, cognitive $\le 15$, function $\le 60$ LOC, $\le 50$ statements. | `praetorctl audit`; all-module `.golangci.yml` gates |
| **HISS-05** | Scoping | Rule 6 | Declare every binding at its smallest lexical scope; no shadowing, no ad-hoc import aliases. | All-module `.golangci.yml`: `govet`, `predeclared`, `reassign`, `importas` |
| **HISS-06** | Concurrency | — | Every goroutine fan-out carries an explicit upper bound. | Required full-tree Semgrep fan-out rule; race tests across 24 modules |
| **HISS-07** | Error Handling | Rule 7 | Every error handled or wrapped with context; no discarded returns. | All-module lint: `errcheck`, `wrapcheck`, `errorlint`, `nilerr`; HISS scan |
| **HISS-08** | Determinism | Rule 8 | No dynamic execution or dynamic code loading; no unsafe libc equivalents. | Required full-tree Semgrep dynamic-code rules; all-module `gosec` |
| **HISS-09** | Reference Safety | Rule 9 | Mandatory `// SAFETY:` proof for every `unsafe` block and pointer cast. | All-module `gosec`; required full-tree Semgrep proof rule; HISS scan |
| **HISS-10** | Warning Hygiene | Rule 10 | Zero-warning tolerance across compiler, linter, and format sweeps. | Required Go, Ruff/Python, Clang/C, docs, Shell, workflow, Terraform, and Kubernetes-schema gates; `make verify-all` mirrors them |
| **HISS-11** | Supply Chain | — | Digest-pinned dependencies, SLSA 3 provenance, cosign signatures, SBOM per release. | Direct attestations currently measure at Build L2; no L3 conformance gate. `cordanaLLM/praetor#330` tracks enforcement; digest, SBOM, cosign, Scorecard, lock gates remain |
| **HISS-12** | Secrets | — | Zero credentials in working tree or git history. | Lefthook staged `gitleaks`; CI full-history `gitleaks` |
| **HISS-13** | Debt Ratchet | — | Total infractions may never increase; baseline only ratchets down. | `.standards-baseline.json` enforced by `praetorctl audit` |
| **HISS-14** | Public ABI | — | Public contracts append-only; any break carries a `Migration:` footer. | Required all-module `apidiff` execution; incompatibilities informational before v1.0; PR checklist |
| **HISS-15** | 3D Testing | Rule 5 | Positive, negative, and boundary tests mandatory for public interfaces. | Race tests across 24 Go modules; 48 Python policy regressions; 70% primary aggregate; shape review-enforced |
| **HISS-16** | Context Integrity | Fleet | Single canonical `AGENTS.md`; vendor files compiled, never hand-edited. | `praetorctl compile-context --verify` in lefthook and `make verify-all` |
| **HISS-17** | State Ledger Discipline | Fleet | `.workingdir/` remains private, ignored, current. | `praetorctl state sync .` post-commit |
| **HISS-18** | Diff-Aware CI Efficiency | Fleet | Run only gates touched by diff. | Not wired for main CI; full matrix runs |
| **HISS-19** | Reuse Before Writing | Fleet | One behavior, one implementation. | `praetorctl dedupe scan .`; cadence recording post-commit |
| **HISS-20** | Enforcement Coverage | Fleet | Every enforcement claim has positive, negative, and gap fixtures. | `praetorctl hiss coverage --verify` replays `.config/hiss/testdata/` |
| **HISS-21** | Platform Neutrality | Fleet | Gates, hooks, and emitted templates run on Linux, macOS, and Windows, or skip with a stated reason; a gate that cannot run is not passing. | Required three-OS `Platform Neutrality` matrix after its first hosted green; local bounded all-module build/vet/short-test gate |

## Operational Rules

1. **Act on verified state.** Read source files, run real commands before hypothesis or edit. Never guess flag names, library signatures, repo configuration from memory.

2. **Lead with output.** Direct answers, diffs, commands. No filler preamble, no "Based on", no restatement, no chatter.

3. **Context transpiler first.** Never edit `CLAUDE.md`, `.cursor/rules/*.mdc`, `.windsurfrules`, `.github/copilot-instructions.md` manually. All agent instruction updates -> `AGENTS.md`, then:

   ```bash
   praetorctl compile-context
   ```

   - `AGENTS.md` = agent-only text -> caveman (internal register). `praetorctl compile-context --verify` + `praetorctl audit` run caveman lint; findings fail gate; no opt-out. Check first: `praetorctl caveman check AGENTS.md`.

4. **SARIF diagnostic distillation.** Compiler/linter errors -> distill to $\le 1,500$ tokens ($< 60$ lines): top 3 root-cause failures with file/line pointers; full SARIF logs -> ephemeral storage.

5. **No evasion.** Never attempt `--no-verify`, `LEFTHOOK=0`, or modifying `.git/hooks`. `cordana-standards[bot]` re-checks every pull request in ephemeral isolated sandbox.

6. **Anti-loop interception.** Same AST diff + error category repeats $\ge 3$ times -> halt immediately. Re-evaluate design; no micro-textual retries.

## Text Register

<!-- praetor:register:start -->
Register follows the audience, then the task label of your brief (`register:` in `.standards.yaml`; labels are the router's `target_tasks`).

| Register | Where | Form |
| :--- | :--- | :--- |
| social | forge: issues, PR bodies, review comments, commit bodies | `social-text` skill: BLUF, full sentences, scannable, enough and no more; conventional commit subject unchanged |
| docs | docs/, README, ADR bodies | complete without bloat: newcomer path first, expert reference after; every claim points at a file, command or test; no restated code |
| internal | briefs, agent-to-agent traffic, research fan-outs, workflow returns | `caveman` skill: fragments, no filler, verbatim code/paths/errors; facts, paths, commands, verdict |

- Task rows: social = commit_message_synthesis, waiver_signoff; docs = architecture_synthesis, function_docstrings; every other label and any unlabeled text = internal. Subagent launch brief: `caveman` brief shape with `task:` = routing label.
- Evidence above 58 lines or 1500 tokens leaves the message as a file under `.workingdir/evidence/`; return `evidence: <path> sha256:<12 hex> lines:<n>` and fetch it only when a decision needs it.
- An internal return carries verdict, changed paths, commands run, evidence pointers and open questions, nothing else.
<!-- praetor:register:end -->

Golusoris social override: no receipt fence; no changelog fragments.
`.github/PULL_REQUEST_TEMPLATE.md` uses `## Summary`; `release-please` reads Conventional
Commits. Generic managed row remains upstream gap `cordanaLLM/praetor#328`.

## Primary Verification Commands

```bash
# Declared application gate
make verify-all

# Cross-agent context verification
praetorctl compile-context --verify

# Declared HISS audit
praetorctl audit

# HISS enforcement-coverage replay
praetorctl hiss coverage --verify
```

<!-- praetor:harness:end -->

---

# Agent Guide — Golusoris

| Topic | Contract |
| :--- | :--- |
| Module | `github.com/golusoris/golusoris` plus lean `github.com/golusoris/golusoris/core` submodule; ADR-0017 |
| Composition | Opt-in `go.uber.org/fx` modules wrapping pinned libraries; applications import capabilities only |
| Catalogue | `README.md` |
| Coding contract | `docs/principles.md` |
| Local context | Read nearest per-subpackage `AGENTS.md` before changes |

## Hard Rules

1. Public API break -> commit body `Migration:` footer with before/after Go snippets; CI runs `apidiff` against previous tag.
2. New transitive dependency -> compare awesome-go alternatives; record non-obvious choice in PR.
3. DI-managed state or lifecycle -> `fx.Module` or `fx.Options`. Stateless
   packages -> constructors or functions. Applications never import internals.
4. Zero `init()` side effects; fx lifecycle hooks own wiring.
5. Errors -> `golusoris/core/errors` or `fmt.Errorf("pkg: op: %w", err)`.
6. Time -> `golusoris/core/clock`; `time.Now()` forbidden elsewhere.
7. Logs -> `golusoris/core/log` slog handler; no `fmt.Println`, no global loggers.
8. Every merged commit -> zero unreviewed lint, gosec, govulncheck findings; race-green. Exact vulnerability exception -> pinned module checksum + patched-source hash + fail-closed policy test. Every `//nolint` needs WHY comment.

## Common Tasks

| Task | Command / Skill |
| :--- | :--- |
| Add fx module | `/wire-fx-module` |
| Add ogen handler stub | `/scaffold-ogen-handler` |
| Add river worker | `/add-river-worker` |
| Add DB migration | `/add-migration` |
| Bump downstream Golusoris | `/bump-golusoris` or `golusoris bump <version>` |
| Skill catalogue | `.claude/skills/` |
| Hook catalogue | `.claude/hooks/README.md` |

## Pinned Upstream Docs

Consult `docs/upstream/` before API suggestions. Public docs may differ from repository pins.

| Package | Pinned version |
| :--- | :--- |
| `go.uber.org/fx` | v1.24.0 |
| `jackc/pgx/v5` | v5.11.0 |
| `ogen-go/ogen` | v1.24.0 |
| `riverqueue/river` | v0.49.0 |
| `knadh/koanf/v2` | v2.3.6 (`core/`) |

Refresh recipe: `make docs-upstream`; full table: `docs/upstream/README.md`.

## CI Contract

- `golangci-lint`: 30+ linters; `.golangci.yml`
- `govulncheck`
- `go test -race -count=1`; merged coverage gate 70%; security-critical target 85%
- Ruff 0.16.8: full-tree Python lint; immutable image
- Python 3.12+: 33 checkpoint + 5 command-policy + 10 portability-policy tests
- Clang/clang-tidy 22.1.8: full-tree C warnings; reproducible checked eBPF object
- All-module `apidiff` versus previous root tag; execution required, incompatibilities informational before v1.0
- Conventional-commit PR title
- Runners: GitHub-hosted `ubuntu-24.04`; no pre-baked tools. Jobs install `tools/tool-versions.env` pins via SHA-pinned installers; cgo headers via `scripts/ci/install-cgo-libs.sh`

## Working Agreements

- Scope: requested behavior only. No speculative features.
- Comments: one-line WHY only. No multi-paragraph commentary.
- Docs: no new Markdown unless explicitly requested.
- Decisions: use `AskUserQuestion` popup where available. (#25)
- Bug state: resolve each fixed, confirmed, or ruled-out item immediately via `praetorctl state bug resolve`. (#26)
- Research/hardening PR: per-module `AGENTS.md`, decision or benchmark digest, STATE delta. (#28)
- Session state: private `.workingdir/{OPEN,STATE,BACKLOG,BUGS,QUESTIONS}.md`; never link or stage.
- Fresh checkout: `praetorctl state init --if-absent`.
- Work completion: `praetorctl state sync .`.
- New or changed module: update README landed list; update AGENTS layout for new top-level package; add per-subpackage `AGENTS.md`.

## HISS-20 Coverage Updates

1. Coverage change -> move or add matching `.config/hiss/testdata/HISS-NN/<language>/{positive,negative,gap}/` fixture. Keep negatives.
2. Update claim `state`; name exact decider in `mechanism`.
3. `rationale` -> measured tool output, never config-only inference.
4. Non-HISS decider -> set `runner`; omitted runner means `praetorctl audit`.
5. Run `make hiss-coverage`; never weaken fixtures for stale claims.
