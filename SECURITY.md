<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Security policy

## Reporting a vulnerability

Please **do not** open public GitHub issues for security vulnerabilities.

Email: `security@lusoris.dev` (or open a private security advisory on GitHub).

We aim to acknowledge within 72 hours and provide a remediation plan within 7 days.

## Supported versions

| Version | Supported | Licence |
| --- | --- | --- |
| `v0.12.x` (current root module) | yes — security fixes land here | EUPL-1.2 |
| `core/v0.9.x` (current `core/` sub-module) | yes — security fixes land here | EUPL-1.2 |
| `v0.8.0` and earlier | no | MIT |

Only the latest minor release of each module is patched; the project is
pre-1.0, so a security fix may ship with a breaking change (called out in
the commit `Migration:` footer and in `docs/migrations/`). Apps should bump
promptly when an advisory is published. Root and `core/` are released by
`release-please` as independent components (`release-please-config.json`)
and no longer move in lockstep: they were tagged together through
`v0.9.0` / `core/v0.9.0`, but from `v0.10.0` on the root module and the
`core/` sub-module version separately, so `v0.12.x` (root) and
`core/v0.9.x` (`core/`) are each the current, supported line for their own
module — watch both tag series, not just the root one. The licence changed
from MIT to EUPL-1.2 at `v0.9.0`; `v0.8.0` and earlier remain MIT. Heavy
sub-modules with their own `go.mod` (`media/*`, `ocr/`, `pdf/`, `hw/*`,
`science/*`, `web3/*`, `testutil/pact`) follow the same patch cadence. CI
builds, vets, lints, scans, and race-tests every module; deployment alerts stay
scoped to apps that import them.

The govulncheck gate (`scripts/ci/govulncheck.sh`) parses the scanner's JSON
and fails on every reachable finding; it carries no exceptions. Invalid or
empty scanner output, an unexpected scanner version or configuration, and
findings without an OSV id or call trace also fail.
`scripts/ci/govulncheck-policy_test.sh` covers each case.

## Supply chain

The blocking `CI success` aggregate in `.github/workflows/ci.yml` covers
formatting; lint, gosec, govulncheck, tidiness, build/vet, API diff, and race
tests across all 27 Go modules; Python and C gates; allocation budgets;
documentation, shell, workflow, Terraform, and Kubernetes checks; Semgrep,
Spectral, dependency review, gitleaks, DCO, and REUSE. DCO is not applicable to
Renovate commits; dependency review and DCO are both skipped outside
pull-request events, and the aggregate validates those skips explicitly.
Main branch protection separately requires the Conventional-Commit PR title and
GitHub-managed CodeQL `Analyze (go)` checks. Repository Actions run on
GitHub-hosted runners (`runs-on: ubuntu-24.04`) with pinned action SHAs; each
job installs its tools at the versions pinned in `tools/tool-versions.env`.

The following controls have enforcement distinct from the blocking
`CI success` aggregate:

- **Semgrep** custom SAST (`.semgrep.yml`) is blocking inside `CI success`.
  The separate `security-scan.yml` registry-rules run is an additional scan.
- `apidiff` reports against the previous root tag but remains informational
  before v1.0
- **CodeQL default setup**, GitHub-managed (languages: Go, Python, GitHub
  Actions; weekly schedule) — its `Analyze (go)` check is a required
  branch-protection check on `main`. It replaces the repository's own
  `codeql.yml` workflow, removed on 2026-08-28 because it could not run on
  the self-hosted ARC runners then in use; default setup runs on GitHub's own
  infrastructure instead
- **OpenSSF Scorecard** as a manual (`workflow_dispatch`) and reusable
  (`workflow_call`) workflow — not triggered on every push, so results are
  not continuously published to the OpenSSF API
- praetor **HISS-21 lattice** governance audit (`praetorctl audit`, ratcheting
  baseline at zero infractions in `.standards-baseline.json`) and the
  six-stage `praetorctl gate run`, run on demand — `make verify-all` composes
  the audit, not the gate. They are local / lefthook gates rather than CI jobs;
  gate wiring stays disabled until the upstream framework-classification fix
  (cordanaLLM/praetor#36)

`.standards.yaml` retains SLSA Build Level 3 as the target policy. The current
root release workflow uses direct GitHub artifact attestations and does not
establish or claim Level 3 conformance. Praetor does not yet measure that
distinction;
[cordanaLLM/praetor#330](https://github.com/cordanaLLM/praetor/issues/330)
tracks audit enforcement.

Releases are:

- Built reproducibly in CI from tagged source by goreleaser (`cmd/golusoris`
  and `cmd/golusoris-mcp`); root and `core/` are tagged independently by
  `release-please` and land on the same commit only when both change
  together
- Signed with [cosign](https://docs.sigstore.dev/cosign/) (keyless, GitHub OIDC)
- Accompanied by per-archive SPDX SBOMs ([syft](https://github.com/anchore/syft)
  via goreleaser). The
  [v0.12.0 release run](https://github.com/golusoris/golusoris/actions/runs/35001126039)
  succeeded and its immutable release carries those SBOM assets. The separate
  source-tree SPDX/CycloneDX workflow is intended to run on each root tag, but
  its [v0.12.0 run](https://github.com/golusoris/golusoris/actions/runs/35001126102)
  failed during Rekor publication. Retry handling is now prospective; do not
  treat source-tree attestations as an every-tag guarantee until a later tag
  proves the repaired path
- Attested with build provenance (`actions/attest-build-provenance`); no SLSA
  level is claimed. Downstream apps can gate deploys with the reusable
  `verify-provenance.yml` workflow, which defaults to checking the keyless
  signature plus SLSA-provenance and SPDX-SBOM predicates against an exact
  commit-pinned signer workflow, source ref, source commit, and image digest.
  GoReleaser stages release assets in a resumable draft; publication happens
  only after the signature and attestations succeed
- Published as [immutable GitHub releases](https://github.blog/changelog/2025-10-28-immutable-releases-are-now-generally-available/)
  (enabled from `v0.10.1` on) — release assets cannot be altered or
  deleted after publication
- Licensed EUPL-1.2 (code) / CC-BY-SA-4.0 (docs) from `v0.9.0`; `v0.8.0`
  and earlier remain MIT

The framework itself ships as archives (no container image); verify a
downloaded release's checksums against the cosign bundle published
alongside it (`checksums.txt` + `checksums.txt.sigstore.json`):

```bash
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/golusoris/golusoris/.github/workflows/release.yml@refs/tags/.*$' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt
```

## Dependencies

Dependency updates are handled by Renovate alone (`renovate.json`):
pin/digest/patch/minor bumps auto-merge once required checks pass, gomod
majors and the Go toolchain directive always require human review, and a
weekly `lockFileMaintenance` run refreshes lockfiles so transitive fixes
land without waiting on a direct-dependency bump. Dependabot is not used
for version updates; its vulnerability alerts remain enabled on the
repository, but Dependabot's automated security-fix pull requests are
turned off so Renovate stays the single updater. The Go toolchain floor is
`go 1.27.2` in every module.

## Framework vs. app responsibility

golusoris ships security primitives, secure defaults, SBOMs, signing, and
provenance attestations. It does not ship a compliance mapping or certify an
assembled application. Apps document and verify their own controls in
`SECURITY.md`; `template/.github/SECURITY.md` is the starting point.
