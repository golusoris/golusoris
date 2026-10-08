<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0021: Run golusoris CI on GitHub-hosted runners

- **Status**: Accepted
- **Date**: 2026-10-08
- **Deciders**: maintainer
- **Tags**: `ci`, `supply-chain`, `governance`

## Context

Every golusoris GitHub Actions job ran on the self-hosted ARC runner set
`arc-cauda-golusoris-golusoris`. The set was undersized for the repository's
lint, build and test cost (ADR-0020 decision 8 records the admin-merge
exception that followed), and on 2026-10-07 it stopped picking up jobs: the
required checks of the Praetor catch-up pull request (#633) stayed queued for
hours, which blocked every merge. The runner image also pre-installed the
release toolchain (gitleaks, cosign, syft, goreleaser, a pinned buildx), so
workflows relied on tools the repository did not declare.

The golusoris organization now has a GitHub Team plan, and the repository is
public, so GitHub-hosted runners are available without capacity planning.

## Decision

All golusoris workflows run on the GitHub-hosted `ubuntu-24.04` label. Tools
the ARC image provided are installed per job from pins in
`tools/tool-versions.env`, through SHA-pinned official installer actions or
checksum-verified release archives (`scripts/ci/install-gitleaks.sh`,
`scripts/ci/install-shellcheck.sh`, both through
`scripts/ci/lib/verified-download.sh`).
System libraries the cgo modules link are installed by
`scripts/ci/install-cgo-libs.sh`. The reusable `ci-go.yml` and
`release-go.yml` default to `ubuntu-24.04` and install declared system
packages instead of only verifying them (input `install-system-packages`).
The live ruleset requires `Test (race + coverage) (ubuntu-24.04, module)`.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
| --- | --- | --- | --- |
| Keep ARC and resize the fleet | Warm caches, no per-job tool installs | Capacity work outside this repository; the fleet was down, not only slow | Blocks every merge until infrastructure outside the repository recovers |
| Mixed: hosted for pull requests, ARC for releases | Release toolchain stays pre-baked | Two runner contracts to maintain; releases still depend on the fleet | One contract is simpler, and the release tools are now declared pins |
| `ubuntu-latest` | No label bumps | The image changes under the repository without review | Pinned label, bumped deliberately |
| `ubuntu-26.04` (used by the Praetor-managed workflows) | Matches the locked Praetor gates | Newer image; the cgo package names were verified on 24.04 | 24.04 for now; revisit with the next image bump |

## Consequences

- **Positive**: required checks run without depending on infrastructure
  outside the repository. Release tools are declared and verified in the
  repository instead of trusted from an image. ADR-0020 decision 8 (admin
  merges while the ARC fleet is constrained) no longer applies.
- **Negative**: each job pays for tool and cgo-library installs and starts
  with a cold Go and image cache (the test job keeps its testcontainers image
  cache through `actions/cache`).
- **Neutral / follow-ups**: dispatch `release-tool-contract.yml` after
  installer pin bumps; the first tag after this change is the proof for the
  release, SBOM and provenance paths. `.gitea/workflows` keep their own runner
  image. Downstream callers of `ci-go.yml` and `release-go.yml` that relied
  on the old default label now run on `ubuntu-24.04`.

## References

- `req`: "so golusoris org has now team for my account, just swap to github runners...." (2026-10-08)
- ADR-0020 decision 8 (admin-merge exception while the ARC fleet was constrained).
- Pull request #633; the runner switch is commit `b109ec3` and its follow-ups.
