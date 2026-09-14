<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# TODO — v0.9.0 (praetor onboarding · lean core · EUPL-1.2)

> Dependency-ordered work graph for the current initiative. Nodes run in
> topological order; siblings in the same phase are independent and run in
> parallel. `[x]` done · `[~]` in progress · `[ ]` pending · `[!]` blocked.
> Branch: `feat/core-submodule-praetor-eupl` (golusoris) ·
> `feat/golusoris-core-onboarding` (cordanallm/praetor). Push target: GitHub.

## Phase 0 — structural moves (done)

- [x] Fast-forward local `main` to `origin/main` (was 192 commits behind).
- [x] Carve `core/` sub-module: config · log · clock · errors · crypto · id ·
      validate · version · clikit · mcp → `github.com/golusoris/golusoris/core/<pkg>`.
      `clikit/tui` stays in the root module (bubbletea kept out of core).
- [x] Rewrite 201 import sites; root + `media/audio` + `media/img` require
      `core v0.9.0` via `replace => ./core`.
- [x] praetor: module path `github.com/cordanaLLM/standards` → `github.com/cordanallm/praetor`
      (56 files); build + tests green.
- [x] Licence texts installed: `LICENSE` = EUPL-1.2; `LICENSES/{EUPL-1.2,CC-BY-SA-4.0,CC-BY-4.0,MIT,Apache-2.0}.txt`.

## Phase 1 — hard bump (everything else builds on the bumped graph)

- [x] 1a `go 1.27.0` directive in root, core, and every sub-module go.mod; `tools/golangci.yml run.go: "1.27"`.
- [ ] 1b `go get -u -t ./... && go mod tidy` in root, core, and all 20 sub-modules.
- [x] 1c Local toolchain: golangci-lint v2.13.2 (CI pin), gofumpt, apidiff, reuse.
- [x] 1d Fix compile/vet breakages from the bump (root + core + sub-modules).
- [x] 1e Gate: `go build ./...` + `go vet ./...` green in every module.

## Phase 2 — parallel workstreams (each depends only on Phase 1)

### 2a core: new packages (praetor `.needs.yaml` gaps)
- [x] `core/codec/yaml` — Marshal/Unmarshal/Strict/Encode/Decode/ReadFile/WriteFile, bounded input (`config.yaml`).
- [x] `core/crypto/receipt` — Ed25519 Exit-0 execution receipts, clock-injected, fx `Module` (`crypto.receipt`).
- [x] `core/gitx` — bounded git runner + repo introspection; `core/gitx/worktree` Manager (`git.worktree`).
- [x] `core/astx` — bounded Go walker, import rewriter (AST), func metrics, go.mod parser (`ast.analyzer`).
- [x] `core/capabilities` — schema + loader + validation for `capabilities.yaml`.
- [x] Tests (table-driven, 3D: positive/negative/boundary), per-package `AGENTS.md`, `core/AGENTS.md`.

### 2b capability contract
- [x] Root `capabilities.yaml` — every package → capability keys (praetor taxonomy + new keys).
- [x] Root `capabilities_test.go` drift guard (listed import exists; every package dir listed).

### 2c licensing (EUPL-1.2 + REUSE, Aegis-OS split)
- [x] `REUSE.toml` — EUPL-1.2 code · CC-BY-SA-4.0 prose · MIT/Apache-2.0 vendored skills · CC-BY-4.0 CoC.
- [x] `LICENSING.md`; README badges + Licence section; `CONTRIBUTING.md` (DCO + inbound=outbound EUPL-1.2).
- [x] `goheader` linter in `tools/golangci.yml` (SPDX template).
- [x] `golusoris init` scaffolds `LICENSE` (EUPL-1.2) + `REUSE.toml` + SPDX headers.

### 2d CI / repo plumbing
- [x] `ci.yml`: core legs for lint · gosec · govulncheck · test(+coverage merge) · build; DCO job; `reuse lint` job.
- [x] `security-scan.yml`: cover `core/`.
- [x] `release-please-config.json`: `core` package, `tag-separator: "/"` → `core/vX.Y.Z`.
- [x] `.github/labeler.yml` + `CODEOWNERS` → `core/**` paths.
- [x] Root `Makefile` (includes `tools/Makefile.shared`, adds `verify-all`, multi-module loops); `.gitignore` (`.standards/`, `coverage*.out`).

### 2e praetor tooling fixes (cordanallm/praetor)
- [x] `internal/compiler`: repo name from `.standards.yaml`, verification commands from manifest/Makefile, per-vendor sections (`## Claude Code`, …) carried from AGENTS.md; budget-checked.
- [x] `internal/needs`: read `<framework>/capabilities.yaml` (fallback to dir heuristics); catalog → `core/` paths + new keys; `sanitizePackageName` major-version noise (`custom.v2`); no hardcoded `/home/kilian` paths; version from `go list -m` / capabilities.yaml (not `v0.8.0`).
- [x] Tests green (`make verify-all`).

## Phase 3 — integration (depends on 2a–2e)

- [x] 3a golusoris adopts praetor governance: `standardsctl adopt --record-baseline` (full), fold `CLAUDE.md` into `AGENTS.md` `## Claude Code`, `compile-context`, `audit` green; `.config/labels.yaml`, `.github/rulesets/main.json`.
- [x] 3b praetor `.needs.yaml` regenerated against `capabilities.yaml` → 100 % readiness; `needs migrate --dry-run` plan saved to praetor `docs/golusoris-migration-plan.md`.
- [x] 3c Fleet report (`needs aggregate`) regenerated → `.workingdir/FLEET-DEMAND.md`.

## Phase 4 — gate (depends on Phase 3)

- [x] `reuse annotate` (per-file SPDX headers) + `reuse lint` clean.
- [x] gofumpt · golangci-lint (root + core) · gosec · govulncheck · tests · coverage ≥ 70 % · apidiff report vs v0.7.0 (breaking, documented).
- [x] praetor `make verify-all` green.

## Phase 5 — docs (depends on Phase 4)

- [x] ADR-0017 lean `core/` sub-module · ADR-0018 EUPL-1.2 + REUSE · ADR-0019 praetor governance adoption.
- [x] `docs/migrations/v0.9.0.md` (import-path codemod) · `changelog.d/` fragments · `CHANGELOG.md` render.
- [x] README (core table, landed list), AGENTS.md tree, `docs/index.md`, `docs/ci-downstream.md`, skills (`wire-fx-module`, `bump-golusoris`), STATE.md session log.

## Phase 6 — commits (signed-off, Conventional Commits)

- [ ] golusoris: carve · bump · core packages + contract · licence · governance · CI/docs.
- [ ] praetor: rename · tooling fixes · needs regen + plan.
- [ ] Release procedure note: tag `core/v0.9.0` then `v0.9.0` on the same commit.

## Added mid-session (user)

- [x] Fleet-wide needs: every Go repo under `~/dev` scanned; `capabilities.yaml` `replaces` + praetor classifier fixes lift coverage to 97.5 %. Real gaps listed in FLEET-DEMAND.md.
- [x] `db/sqlite` (modernc) — 2 fleet consumers (VMAFx, cauda.dev/knowledge-mcp).

## Deferred (next session)

- praetor code migration onto core (yaml.v3 → codec/yaml, flag → clikit, JSON-RPC → mcp, receipts, worktree, slog).
- Gitea canonical remote (user: later).
- Other fleet repos' migration (VMAFx on v0.7.0 → v0.9.0 core paths).
