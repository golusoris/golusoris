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
|---|---|---|
| `v0.10.x` (current root module) | yes — security fixes land here | EUPL-1.2 |
| `core/v0.9.x` (current `core/` sub-module) | yes — security fixes land here | EUPL-1.2 |
| `v0.8.0` and earlier | no | MIT |

Only the latest minor release of each module is patched; the project is
pre-1.0, so a security fix may ship with a breaking change (called out in
the commit `Migration:` footer and in `docs/migrations/`). Apps should bump
promptly when an advisory is published. Root and `core/` are released by
`release-please` as independent components (`release-please-config.json`)
and no longer move in lockstep: they were tagged together through
`v0.9.0` / `core/v0.9.0`, but from `v0.10.0` on the root module and the
`core/` sub-module version separately, so `v0.10.x` (root) and
`core/v0.9.x` (`core/`) are each the current, supported line for their own
module — watch both tag series, not just the root one. The licence changed
from MIT to EUPL-1.2 at `v0.9.0`; `v0.8.0` and earlier remain MIT. Heavy
sub-modules with their own `go.mod` (`media/*`, `ocr/`, `pdf/`, `hw/*`,
`science/*`, `web3/*`, `testutil/pact`) are patched on the same cadence but
only when a consumer exists — alerts there are scoped to apps that import
them.

## Supply chain

Every merged commit passes (`.github/workflows/ci.yml`, all jobs on
self-hosted [ARC](https://github.com/actions/actions-runner-controller)
runners — `runs-on: arc-cauda-golusoris-golusoris` — with pinned action SHAs):

- `golangci-lint` (30+ linters incl. `gosec`-adjacent checks), standalone
  `gosec`, `govulncheck`, `go test -race`, `apidiff` vs the previous tag
- **Semgrep** custom SAST (`.semgrep.yml`; also the standalone
  `security-scan.yml` run inside the pinned `semgrep/semgrep` container)
- **gitleaks** secret scan (`.gitleaks.toml`) on every PR and, via lefthook,
  on every staged commit
- **DCO** `Signed-off-by:` on every commit and **`reuse lint`** on every
  first-party file (SPDX headers + `REUSE.toml`; see
  [LICENSING.md](LICENSING.md)) — the licence of every file is
  machine-readable for SBOM accuracy
- Dependency review on every PR
- **CodeQL default setup**, GitHub-managed (languages: Go, Python, GitHub
  Actions; weekly schedule) — its `Analyze (go)` check is a required
  branch-protection check on `main`. It replaces the repository's own
  `codeql.yml` workflow, removed on 2026-08-28 because it could not run on
  the self-hosted ARC runners; default setup runs on GitHub's own
  infrastructure instead
- **OpenSSF Scorecard** as a manual (`workflow_dispatch`) and reusable
  (`workflow_call`) workflow — not triggered on every push, so results are
  not continuously published to the OpenSSF API
- praetor **HISS-16** governance audit (`standardsctl audit`, ratcheting
  baseline at zero infractions in `.standards-baseline.json`) and
  `standardsctl gate run` (stages 1–3) through `make verify-all` — both are
  local / lefthook gates today rather than CI jobs; gate's stage 4 (flavor
  conformance) and the hook wiring for `gate` itself stay disabled until an
  upstream praetor classification fix (cordanaLLM/praetor#36)

Releases are:

- Built reproducibly in CI from tagged source by goreleaser (`cmd/golusoris`
  and `cmd/golusoris-mcp`); root and `core/` are tagged independently by
  `release-please` and land on the same commit only when both change
  together
- Signed with [cosign](https://docs.sigstore.dev/cosign/) (keyless, GitHub OIDC)
- Accompanied by a per-archive SPDX SBOM ([syft](https://github.com/anchore/syft),
  via goreleaser) plus source-tree SPDX and CycloneDX SBOMs published as
  GitHub attestations and workflow artifacts on every tag
  (`.github/workflows/sbom.yml`, `actions/attest-sbom`)
- Attested with [SLSA](https://slsa.dev/) L3 provenance
  (`actions/attest-build-provenance`); downstream apps can gate deploys
  with the reusable `verify-provenance.yml` workflow
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
`go 1.27.1` in every module.

## Framework vs. app responsibility

golusoris ships the scaffolding (SBOM, signing, provenance, secure defaults,
compliance anchors in [docs/principles.md](docs/principles.md) §2.5). Apps
assert their own compliance posture in their `SECURITY.md`; the
`template/.github/SECURITY.md` stub is the starting point.
