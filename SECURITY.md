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
| `v0.9.x` (current, root + `core/` sub-module) | yes — security fixes land here | EUPL-1.2 |
| `v0.7.x` (latest tag; `v0.8.0` is prepared in `CHANGELOG.md` but not yet tagged) | until `v0.9.0` is tagged, then no | MIT |
| `< v0.7.0` | no | MIT |

Only the latest minor release is patched; the project is pre-1.0, so a
security fix may ship with a breaking change (called out in the commit
`Migration:` footer and in `docs/migrations/`). Apps should bump promptly
when an advisory is published. `core/` is tagged in lockstep
(`core/vX.Y.Z` on the same commit as `vX.Y.Z`) and shares this policy.
Heavy sub-modules with their own `go.mod` (`media/*`, `ocr/`, `pdf/`,
`hw/*`, `science/*`, `web3/*`, `testutil/pact`) are patched on the same
cadence but only when a consumer exists — alerts there are scoped to apps
that import them.

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
- Dependency review and OpenSSF Scorecard (CodeQL was retired on
  2026-08-28 — it cannot run on the self-hosted runners)
- praetor **HISS-16** governance audit (`standardsctl audit`, ratcheting
  baseline in `.standards-baseline.json`) through `make verify-all` —
  today a local / lefthook gate rather than a CI job

Releases are:

- Built reproducibly in CI from tagged source by goreleaser (root and
  `core/` tagged on the same commit)
- Signed with [cosign](https://docs.sigstore.dev/cosign/) (keyless, GitHub OIDC)
- Accompanied by a [syft](https://github.com/anchore/syft) SBOM
- Attested with [SLSA](https://slsa.dev/) L3 provenance
  (`actions/attest-build-provenance`); downstream apps can gate deploys
  with the reusable `verify-provenance.yml` workflow
- Licensed EUPL-1.2 (code) / CC-BY-SA-4.0 (docs) from `v0.9.0`; `v0.8.0`
  and earlier remain MIT

Verify a release container:

```bash
cosign verify ghcr.io/golusoris/golusoris:vX.Y.Z   --certificate-identity-regexp '^https://github.com/golusoris/golusoris/'   --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

## Dependencies

Tracked by Renovate (routine bumps, grouped per ecosystem) + Dependabot
(security alerts). Auto-merge on green CI for minor/patch; majors require
human review. The Go toolchain floor is `go 1.27.0` in every module.

## Framework vs. app responsibility

golusoris ships the scaffolding (SBOM, signing, provenance, secure defaults,
compliance anchors in [docs/principles.md](docs/principles.md) §2.5). Apps
assert their own compliance posture in their `SECURITY.md`; the
`template/.github/SECURITY.md` stub is the starting point.
