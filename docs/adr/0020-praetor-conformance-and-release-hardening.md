<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0020: Second-wave praetor conformance and release hardening

- **Status**: Proposed
- **Date**: 2026-09-15
- **Deciders**: @lusoris
- **Tags**: governance, ci, security, releases, praetor

## Context

[ADR-0019](0019-praetor-governance-and-capability-contract.md) adopted
cordanaLLM/praetor governance in full but left several gaps open: praetor's
own gate (`internal/gating/pipeline.go`) refused to run at all because
`.golangci.yml` and `.gosec.json` lived under `tools/` instead of the repo
root; the checked-in `.devcontainer/` referenced a `docker/dev/Dockerfile`
that did not exist; `.workingdir/` was tracked in git with a `.gitignore`
comment declaring that intentional, which conflicts with praetor's HISS-17
("State Ledger Discipline": the directory must be private and git-ignored
fleet-wide); praetor's own lefthook scaffold duplicated golusoris-specific
hook logic; the branch protection enforced on GitHub was hand-maintained
classic protection rather than a declared policy; and the release pipeline
had no immutability or source-tree SBOM guarantees to match the
`supply_chain` block (`slsa_level: 3`, `enforce_cosign`, `require_sbom`)
already declared in `.standards.yaml`. Downstream apps also consumed the
framework's reusable workflows (`ci-go.yml`, `release-go.yml`,
`scorecard.yml`) through the `template/` scaffold, which had drifted onto a
broken pin. This ADR records the conformance and release-hardening work
that closed those gaps on 2026-09-14 and 2026-09-15, tracked under epic
issue #429 and its per-area tasks #432–#434.

## Decision

1. **`.workingdir/` becomes private (HISS-17).** The directory (12 tracked
   files: routing ledger, `PLAN.md`/`STATE.md`/`QUESTIONS.md`, archive) is
   untracked and `.gitignore` gains praetor's exact `/.workingdir/` rule plus
   a nested `.workingdir/` rule for per-package copies (`#495`). This
   reverses a convention that had been declared only in a `.gitignore`
   comment, not in any Accepted ADR — no ADR is superseded by this change.
   Session state that previously lived in tracked `.workingdir/` files now
   persists outside the repository.
2. **Praetor's lefthook jobs are merged in, not swapped in wholesale
   (HISS-19).** `context-check`, `hiss-audit`, the `agent-checkpoint-*`
   jobs, post-commit `state-sync`/`dedupe-cadence`, and the pre-push
   `security`/`audit`/`gate` jobs are adopted verbatim from
   `standardsctl adopt --lock-source-root=<praetor>`. golusoris-specific
   scripts that cover the same ground at least as strictly are kept instead
   of praetor's generic equivalents: `scripts/hooks/gofumpt.sh` (a verified
   strict superset of praetor's plain `gofmt -w`) and
   `scripts/hooks/govet.sh` (iterates every `go.mod` in the multi-module
   tree; a bare `go vet ./...` does not). Two pre-push jobs, `flavor-audit`
   and `gate`, are wired into `lefthook.yml` but held back (commented out of
   the active `commands:` block) because praetor misclassifies this
   library's `go.mod` + `cmd/` layout as the `go-service` flavor and demands
   artifacts a library does not need (cordanaLLM/praetor#36); `standardsctl
   gate run --path=.` already passes stages 1–3 (lockfile, HISS scan,
   security) and fails only stage 4, the flavor check the held-back jobs
   would run (`#495`).
3. **Canonical config paths.** `.golangci.yml` and `.gosec.json` move to the
   repository root, the paths praetor's flavor definitions and gate require
   (`#494`). The prior `tools/gosec.exclude-rules` path-scoped exclusion list
   is replaced by inline `// #nosec Gxxx -- reason` comments at each finding,
   which — unlike the exclusion file — are recognized by both `gosec` and
   golangci-lint's embedded gosec linter from the same annotation.
4. **The devcontainer is regenerated from praetor's source bundle.** The
   previous `devcontainer.json` built from a `Dockerfile` that was never
   committed, so the container had been broken since it was added.
   `standardsctl devcontainer --source-root=<praetor> --force` (praetor
   `ccfe6f0`) replaces it with `Dockerfile.praetor` and a three-part base64
   source bundle that builds `praetorctl` inside the container (`#493`).
5. **A branch ruleset is declared with `single_maintainer` review mode.**
   `.standards.yaml` sets `branch_protection.review_mode:
   single_maintainer`; praetor renders this to zero required approving
   reviews and no code-owner review, matching praetor's own dogfooded
   policy for a solo maintainer. `.github/rulesets/main.json` is regenerated
   from that declared policy by `standardsctl sync`: required status checks
   Build, Security (gosec), Lint, Test (race + coverage) and Vulnerabilities
   (govulncheck), strict status checks, linear history, required signatures,
   no bypass actors (`#509`). Applying the ruleset to GitHub
   (`standardsctl sync --remote`) is deferred until the fleet-gated pull
   requests already in flight land, since a ruleset carries no admin bypass;
   until then GitHub enforces the pre-existing classic branch protection
   (required checks "CI success", "PR title (Conventional Commits)",
   "Analyze (go)"; strict; linear history).
6. **Releases are immutable, with source-tree SBOM attestations.** GitHub
   immutable releases are enabled for this repository (`v0.10.1` is the
   first release published under it). Because an immutable release accepts
   no assets after publication, `sbom.yml` generates SPDX and CycloneDX
   SBOMs for the tagged source tree and publishes them as GitHub
   attestations (`actions/attest-sbom`, Sigstore keyless) and workflow
   artifacts, rather than as release-asset uploads (`#508`). This is in
   addition to the per-archive SPDX SBOMs `release.yml` already emits
   through goreleaser and signs with cosign keyless signatures and
   `actions/attest-build-provenance`.
7. **`template/` pins the reusable workflows at a tagged commit, not a
   floating ref.** `template/.github/workflows/{ci,release}.yml` reference
   `golusoris/golusoris/.github/workflows/{ci-go,release-go,scorecard}.yml`
   by full commit SHA with a trailing `# vX.Y.Z` comment
   (`380b26797a8552c8b8aba03d53209b8997f2b1be # v0.10.1`), corrected onto the
   current tagged release after an earlier automated dependency bump had
   landed the template on a broken pin (`#500`).
8. **Admin merges on a green local gate are a documented, commented
   exception while the ARC fleet is capacity-constrained.** The self-hosted
   `arc-cauda-golusoris-golusoris` runner set is undersized for this
   monorepo's lint/build/test cost under contention (`ci.yml`'s Lint job
   comment records a measured ~810 CPU-s cold-cache cost against a
   750 m-CPU-guaranteed runner, and a starved node pushing a run past the
   original 20-minute budget). With a single maintainer and `review_mode:
   single_maintainer` in effect, a merge is admin-merged only after the
   documented local gate (`make verify-all`) has passed on the branch tip,
   and the PR or commit records that as the reason; this is a stated
   exception to waiting on GitHub's required checks, not a substitute for
   the required checks once the ARC fleet resize referenced in the `ci.yml`
   comment lands.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Keep `.workingdir/` tracked, ask praetor to special-case golusoris | No git-history churn; state stays inspectable in-tree | HISS-17 is a fleet-wide invariant enforced by praetor's own Git metadata gate; a per-repo exception defeats "one behavior, one implementation" | Fleet-wide conformance (HISS-19) over a local carve-out |
| Replace `lefthook.yml` wholesale with praetor's generated scaffold | Zero merge work, always in sync with praetor | Drops golusoris's stricter multi-module `govet.sh` and gofumpt-over-gofmt reasoning; silently loses coverage in a 21-module monorepo | Verified check-by-check merge, kept where golusoris's script covers strictly more |
| Suppress the flavor mismatch (patch praetor's flavor detector locally, or fake the manifest) | `flavor-audit`/`gate` could stay enabled today | Masks a real upstream bug (praetor#36) instead of fixing it; a local patch on a fleet-wide tool re-diverges the fleet | Held back with an explicit re-enable condition tied to the upstream fix |
| Leave `.golangci.yml`/`.gosec.json` under `tools/` and symlink from root | Avoids repointing every consumer | Praetor's flavor definitions look for the file at the root path, not a symlink target, by name; `standardsctl gate run` failed before reaching the security scan | Canonical root paths, every consumer repointed |
| Mutable releases, SBOMs attached as release assets | One artifact surface (the release page) | Immutable releases (once enabled) reject post-publication asset uploads outright | GitHub attestations for source-tree SBOMs instead of release assets |
| Apply the new ruleset to GitHub immediately | Enforcement matches the declared policy right away | Rulesets carry no admin bypass; an in-flight fleet-gated PR that cannot yet satisfy every declared check would be locked out with no override | Declare the policy now, apply it once in-flight PRs land |
| Block every merge on ARC-backed required checks finishing, regardless of queue depth | Uniform enforcement, no manual step | The fleet is measurably undersized today; a solo maintainer with a green local gate would otherwise be blocked by runner contention alone | Documented, commented admin-merge exception until the fleet is resized |

## Consequences

- **Positive**: `standardsctl audit` runs green against the HISS-16 baseline
  (0 since #503); `standardsctl gate run` passes stages 1–3; the devcontainer
  builds and `standardsctl devcontainer --verify` passes; the branch
  protection policy is declared in a file (`.github/rulesets/main.json`)
  instead of only existing as a GitHub UI setting; releases carry SLSA-3
  provenance, cosign signatures and both per-archive and source-tree SBOM
  attestations; downstream apps pin reproducible, verifiable workflow
  versions instead of a floating branch ref.
- **Negative**: `flavor-audit` and `gate` stay disabled in `lefthook.yml`
  pending an external fix (cordanaLLM/praetor#36), so stage 4 (flavor
  conformance) of praetor's gate is not locally enforced until then;
  `.workingdir/` state is no longer versioned in git, so continuity across
  sessions depends entirely on tooling outside the repository; the declared
  ruleset is not yet the ruleset GitHub enforces, so the two can drift out
  of sync until `standardsctl sync --remote` runs; the admin-merge exception
  is a human judgment call recorded in prose (PR/commit rationale), not a
  machine-checked gate.
- **Neutral / follow-ups**: apply `.github/rulesets/main.json` to GitHub via
  `standardsctl sync --remote` once the fleet-gated pull requests in flight
  land (tracked in epic task #434); re-enable `flavor-audit` and `gate` once
  cordanaLLM/praetor#36 is fixed and `praetorctl flavor audit .` exits 0 on
  `main`; retire the admin-merge exception once the ARC runner resize noted
  in the `ci.yml` Lint job comment is applied.

## References

- [ADR-0017](0017-lean-core-submodule.md) · [ADR-0019](0019-praetor-governance-and-capability-contract.md)
- PRs: #484 (drop retired CodeQL job, restore Scorecard), #487 (`runs-on`/`working-directory` inputs for `ci-go.yml`), #488 (praetor agent harness), #492 (skip container-backed testutil helpers under `-short`), #493 (devcontainer regeneration, HISS-01 recursion), #494 (canonical `.golangci.yml`/`.gosec.json` paths, inline `#nosec`), #495 (praetor lefthook adoption, `.workingdir/` untracked), #496 (release v0.10.0), #498 (`ci-go.yml` module-matrix fix), #499 (release v0.10.1), #500 (template pin corrected to v0.10.1), #503 (HISS baseline burned down to 0), #506 (release v0.10.2), #508 (source-tree SBOM attestations), #509 (branch ruleset declaration)
- Upstream: cordanaLLM/praetor#36 (flavor misdetection for a `go.mod` + `cmd/` library)
- Epic: #429; tasks #432, #433, #434
- HISS-17/HISS-18/HISS-19 specifications: <https://standards.cordana.ai/standards/>
