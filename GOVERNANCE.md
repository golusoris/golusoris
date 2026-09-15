<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Governance

This document describes how **golusoris** — a composable, opt-in `fx`-module
framework for Go services — is governed.

## 1. Project scope

golusoris ships foundational building blocks (config, logging, clock, DB, HTTP,
jobs, cache, auth, observability, …) as independent `fx` modules that apps
compose à la carte. The framework's binding constraints (tracked day-to-day in
the maintainers' private, git-ignored `.workingdir/PLAN.md`, per HISS-17) are:

- Power-of-10 (Go-adapted) hard gates, SEI CERT for Go, Google Go Style.
- Zero lint / zero gosec / zero govulncheck, race-clean, on every merged commit.
- Architecture decisions recorded as ADRs under [`docs/adr/`](docs/adr/).
- Supply-chain + compliance posture (SLSA L3, OWASP ASVS L2, NIST SSDF, EU CRA,
  OpenSSF) asserted in [`SECURITY.md`](SECURITY.md).
- Licensing: EUPL-1.2 for code, CC-BY-SA-4.0 for prose, REUSE-compliant, DCO
  sign-off ([ADR-0018](docs/adr/0018-eupl-relicense-and-reuse.md),
  [`LICENSING.md`](LICENSING.md)).

These are non-negotiable: a change that regresses a hard gate does not merge.

### 1.1 Governance harness — the praetor HISS-20 lattice

Since v0.9.0 the contract is machine-enforced by
[cordanallm/praetor](https://github.com/cordanallm/praetor)
([ADR-0019](docs/adr/0019-praetor-governance-and-capability-contract.md)),
which layers five fleet-wide invariants (HISS-16 to HISS-20) on top of the
fifteen numbered Power-of-10 gates (HISS-01 to HISS-15) in
[`AGENTS.md`](AGENTS.md) — twenty invariants in all, each row there naming the
gate that enforces it in this repository:

- **HISS-16 (Context Integrity)**: [`AGENTS.md`](AGENTS.md) is the **single
  canonical agent harness**. Every vendor context file (`CLAUDE.md`,
  `.cursor/`, `.gemini/`, `.codex/`, `.windsurfrules`, IDE configs) is
  compiled from it by `standardsctl compile-context` and verified by the
  lefthook pre-commit hook and `make verify-all` — never edited by hand.
- **HISS-17 (State Ledger Discipline)**: the private `.workingdir/` ledger
  (§1 above) is kept current with `standardsctl state task`, `state bug`
  and `state question`; the lefthook post-commit hook runs
  `standardsctl state sync` after every commit.
- **HISS-18 (Diff-Aware CI Efficiency)**: praetor's `standardsctl ci filter`
  scopes a change's gates to what its diff actually touches. **It is not wired
  here**: `.github/workflows/ci.yml` has no filter step and no `paths:`
  restriction, so every pull request still runs the full matrix.
- **HISS-19 (Reuse Before Writing)**: `standardsctl dedupe scan` (wired as
  `make dedupe-scan` and the lefthook post-commit `dedupe-cadence` job)
  flags a second implementation of a behavior — including a second
  configuration format — that already exists elsewhere in the tree.
- **HISS-20 (Enforcement Coverage Catalogue)**: a repository declares, per rule
  and per language, what its gates actually detect, and `standardsctl hiss
  coverage --verify` replays that claim against a fixture corpus in both
  directions. **Not declared here yet**: there is no `.config/hiss/coverage.yaml`,
  so enforcement evidence is undeclared for every invariant.

`standardsctl audit` scores the tree against the HISS invariants (the
modernised Power-of-10 table at the top of `AGENTS.md`) with a ratcheting
baseline in `.standards-baseline.json`; a change may not regress it.
`standardsctl gate run` runs praetor's four-stage conformance gate
(lockfile digests, the HISS scan, a security pass, flavor conformance);
stages 1-3 pass on `main` today, but stage 4 is held back because praetor
misclassifies this library as a `go-service` and demands artifacts a
library does not carry (a `Dockerfile`, an `on:`-less `security.yml`) —
tracked upstream as
[cordanaLLM/praetor#36](https://github.com/cordanaLLM/praetor/issues/36).
[`capabilities.yaml`](capabilities.yaml) is the machine-readable capability
contract that praetor `needs` resolves fleet demand against; the root
`capabilities_test.go` guards it against tree drift.

The hook ladder runs through
[lefthook](https://github.com/evilmartians/lefthook) with praetor's
configuration ([`lefthook.yml`](lefthook.yml)): pre-commit (formatting,
lint, `compile-context --verify`, `audit`, `reuse lint`, `gitleaks`),
commit-msg (Conventional Commits + DCO), post-commit (`state sync`,
`dedupe cadence`), and pre-push (build + `go test -short`, `govulncheck`,
`audit`) — the pre-push `gate` and `flavor-audit` jobs stay held back for
the same praetor#36 reason as above. [`make verify-all`](Makefile) runs the
equivalent build, lint, test, capability-drift, `compile-context --verify`,
`audit`, `dedupe-scan` and `reuse lint` gates in one command — the one
command every maintainer and agent runs before concluding a change.

## 2. Roles

### 2.1 Benevolent Dictator (BDFL)

The project is maintained under a **BDFL** model. The BDFL has final say on:

- Architectural decisions captured as ADRs under [`docs/adr/`](docs/adr/).
- Acceptance of code into `main`.
- Release cadence and SemVer semantics.
- Security advisories and coordinated disclosure.

The current BDFL is listed in [`MAINTAINERS.md`](MAINTAINERS.md).

### 2.2 Maintainers

Maintainers have **write access** and CODEOWNERS responsibility for specific
subtrees (see [`.github/CODEOWNERS`](.github/CODEOWNERS)). They are listed in
[`MAINTAINERS.md`](MAINTAINERS.md) with the subtrees they own. A maintainer:

- Reviews PRs that touch their owned subtree.
- Keeps the subtree's `AGENTS.md` (per-package invariants) in sync with the code.
- Triages issues filed against their subtree.

### 2.3 Contributors

Anyone who opens an issue or PR is a contributor. There is no CLA. Inbound =
outbound: contributions are licensed under the terms of the material they
touch — [`EUPL-1.2`](LICENSE) for code, `CC-BY-SA-4.0` for prose (see
[`LICENSING.md`](LICENSING.md)) — and every commit certifies the
[Developer Certificate of Origin](https://developercertificate.org/) with a
`Signed-off-by:` trailer (`git commit -s`). The CI `DCO sign-off` job fails on
unsigned commits.

## 3. Decision-making

### 3.1 Architectural decisions — ADRs

Every non-trivial architectural, policy, or scope decision lands as an
Architecture Decision Record under [`docs/adr/`](docs/adr/) **before** the
implementing commit, following Michael Nygard's template (Status / Context /
Decision / Alternatives considered / Consequences / References). Once an ADR's
Status flips to **Accepted**, its body is immutable — a superseding decision
gets a new ADR that links back via `Supersedes`.

### 3.2 Routine changes — PRs

Bug fixes and implementation work flow through pull requests against `main`.
Every PR must satisfy:

- Conventional Commits (`type(scope): subject`).
- DCO `Signed-off-by:` trailer on every commit.
- The status checks GitHub requires on `main` today (classic branch
  protection, `strict` — the branch must be up to date — with linear
  history required): **CI success** (lint, gosec, govulncheck, race tests,
  build, `reuse lint`), **PR title (Conventional Commits)**, and
  **Analyze (go)** — the required job of GitHub's CodeQL default setup,
  which replaced the self-hosted `codeql.yml` workflow on 2026-08-28. The
  advisory jobs (apidiff, Semgrep, gitleaks, DCO, changelog fragments) run
  alongside but are not required checks.

praetor's target protection is declared, not yet enforced, as a GitHub
ruleset in [`.github/rulesets/main.json`](.github/rulesets/main.json)
(#509): per-job required checks (`Build`, `Security (gosec)`, `Lint`,
`Test (race + coverage)`, `Vulnerabilities (govulncheck)`), required
signed commits, and a pull-request rule under **`review_mode:
single_maintainer`** ([`.standards.yaml`](.standards.yaml)). That mode
keeps the framework's declared `required_approving_reviewers: 1` on
record but renders it as `0` required approvals with no code-owner review
today, because the project has one maintainer who cannot approve their own
PR; switching to independent review once a second maintainer or an
approving review bot exists is then a config change, not a rewrite.

Classic protection does not set `enforce_admins`, so the BDFL can merge a
PR past a required check that never ran. That bypass is reserved for one
situation — the self-hosted ARC runner fleet
(`arc-cauda-golusoris-golusoris`) is unavailable and cannot produce the
check — and the merge must carry a comment on the PR naming the check that
could not run and why, before the merge happens.

### 3.3 Disagreements

Reasonable disagreements about an ADR or PR are resolved in the PR / ADR thread.
Where consensus is not reached, the BDFL decides and the rationale is captured
in the ADR's `## References` section.

## 4. Releases

[release-please](.github/workflows/release-please.yml) opens and updates a
release pull request against `main` on every push, following
[SemVer](https://semver.org) and
[Keep a Changelog](https://keepachangelog.com). Its manifest
([`.release-please-manifest.json`](.release-please-manifest.json)) tracks
the root module and the `core/` sub-module
([ADR-0017](docs/adr/0017-lean-core-submodule.md)) as separate components;
the workflow runs with `skip-github-release: true`, so it only prepares the
PR and changelog. Once that PR is merged, the `vX.Y.Z` tag is pushed
explicitly on the merge commit; `core/vX.Y.Z` is pushed alongside it only when
the `core/` sub-module changed in that release, so the two tags share a commit
only in that case (v0.10.0 and core/v0.9.1 do, v0.10.1 has no core tag). The
project is pre-1.0:
breaking changes are permitted between minor versions and called out in the
commit `Migration:` footer and the changelog.

Pushing the root `vX.Y.Z` tag triggers
[`release.yml`](.github/workflows/release.yml) (goreleaser): multi-arch
archives for `cmd/golusoris` and `cmd/golusoris-mcp`, a checksum manifest,
per-archive SPDX SBOMs (syft), cosign keyless signatures, and SLSA build
provenance (`actions/attest-build-provenance`); and
[`sbom.yml`](.github/workflows/sbom.yml), which additionally attests
source-tree SPDX and CycloneDX SBOMs of the tagged commit
(`actions/attest-sbom`) and uploads them as workflow artifacts. Releases on
this repository are immutable (enabled from `v0.10.1` onward): once
published, a release's tag, assets and metadata cannot be edited or
deleted. Only the root tag ever produces a GitHub Release — `core/vX.Y.Z`
is a Go-module version tag with no Release object of its own — so GitHub's
"latest release" marker, and the `.../releases/latest` API endpoint that
downstream apps and CI resolve against, always track the root module.

Downstream apps can gate deploys on the provenance attestations via the
reusable [`verify-provenance.yml`](.github/workflows/verify-provenance.yml)
workflow. See [`SECURITY.md`](SECURITY.md) for the full supply-chain
guarantees.

## 5. Security

Vulnerability reports follow the coordinated-disclosure flow in
[`SECURITY.md`](SECURITY.md). Public issues are **not** the right channel —
use GitHub's private vulnerability-reporting form or the email channel listed
there.

## 6. Code of Conduct

All community interactions are governed by
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md), adapted from the Contributor
Covenant. Enforcement is the responsibility of maintainers.

## 7. Amending this document

Changes to this `GOVERNANCE.md` follow the normal PR flow. Substantial
governance shifts (e.g. moving from BDFL to a steering committee) require an ADR
that cites this file under `## References`.
