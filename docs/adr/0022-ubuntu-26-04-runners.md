<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0022: Run golusoris CI on ubuntu-26.04 GitHub-hosted runners

- **Status**: Accepted
- **Date**: 2026-10-17
- **Deciders**: maintainer
- **Tags**: `ci`, `supply-chain`, `governance`

## Context

ADR-0021 moved golusoris CI to GitHub-hosted runners pinned to `ubuntu-24.04`.
The repository policy is to track the newest working GitHub-hosted Ubuntu
image. Praetor-managed workflows (`praetor-api.yml`, `praetor-docs.yml`,
`portability.yml`, `reuse.yml`) are already running on `ubuntu-26.04`.

Previously, runner labels were embedded in required status check contexts,
which made runner bumps high-friction. Pull request #934 removed the runner
label from required check names (such as `Test (race + coverage) (module)`),
allowing future image bumps to land as ordinary pull requests without
branch-protection or ruleset renames.

## Decision

Supersede the runner-label specification of ADR-0021: all golusoris workflows
and reusable workflow defaults move from `ubuntu-24.04` to `ubuntu-26.04`,
and the arm64 image smoke job moves from `ubuntu-24.04-arm` to
`ubuntu-26.04-arm`.
The rule remains: use the newest working GitHub-hosted Ubuntu image.

System package requirements and cgo dependencies declared in
`scripts/ci/install-cgo-libs.sh` continue to install using non-release-specific
Debian package names.

## Consequences

- **Positive**: Workflows run on the newest working GitHub-hosted Ubuntu
  image, aligning repository CI with Praetor-managed gates. Future bumps are
  ordinary PRs because required checks do not embed the runner label (#934).
- **Neutral**: Jobs continue installing declared tools and cgo libraries per
  job from pinned specifications.

## References

- Issue #815 step 3 (migrate workflows to ubuntu-26.04).
- Pull request #934 (remove runner label from required status check names).
- ADR-0021 (Run golusoris CI on GitHub-hosted runners).
