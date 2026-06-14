# Governance

This document describes how **golusoris** — a composable, opt-in `fx`-module
framework for Go services — is governed.

## 1. Project scope

golusoris ships foundational building blocks (config, logging, clock, DB, HTTP,
jobs, cache, auth, observability, …) as independent `fx` modules that apps
compose à la carte. The framework's binding constraints are encoded in
[`.workingdir/PLAN.md` §2](.workingdir/PLAN.md):

- Power-of-10 (Go-adapted) hard gates, SEI CERT for Go, Google Go Style.
- Zero lint / zero gosec / zero govulncheck, race-clean, on every merged commit.
- Architecture decisions recorded as ADRs under [`docs/adr/`](docs/adr/).
- Supply-chain + compliance posture (SLSA L3, OWASP ASVS L2, NIST SSDF, EU CRA,
  OpenSSF) asserted in [`SECURITY.md`](SECURITY.md).

These are non-negotiable: a change that regresses a hard gate does not merge.

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

Anyone who opens an issue or PR is a contributor. There is no CLA — by
submitting code, contributors agree to license it under the terms in
[`LICENSE`](LICENSE) (MIT).

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
- The framework's required status checks (lint, gosec, govulncheck, race tests,
  build, CodeQL). `main` is host-protected with `enforce_admins` on — no
  bypass, including for the BDFL.

### 3.3 Disagreements

Reasonable disagreements about an ADR or PR are resolved in the PR / ADR thread.
Where consensus is not reached, the BDFL decides and the rationale is captured
in the ADR's `## References` section.

## 4. Releases

Releases are automated by [release-please](.github/workflows/release-please.yml)
on pushes to `main`, following [SemVer](https://semver.org) and
[Keep a Changelog](https://keepachangelog.com). The project is pre-1.0:
breaking changes are permitted between minor versions and called out in the
commit `Migration:` footer and the changelog.

Every tagged release ships SPDX SBOMs (syft), cosign keyless signatures, and
SLSA build provenance (`actions/attest-build-provenance`). Downstream apps can
gate deploys on these via the reusable
[`verify-provenance.yml`](.github/workflows/verify-provenance.yml) workflow. See
[`SECURITY.md`](SECURITY.md) for the full supply-chain guarantees.

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
