# Contributing

## Conventional commits

All commits and PR titles MUST follow [Conventional Commits](https://www.conventionalcommits.org/):

```text
<type>(<scope>): <subject>

[optional body]

[optional footer(s)]
```

Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`,
`ci`, `revert`.

Scopes are subpackage names: `feat(jobs):`, `fix(db/pgx):`, `chore(tools):`.

Release Please is the sole changelog writer. Put every user-visible change in
a release-visible Conventional Commit message (`feat`, `fix`, `perf`,
`revert`, or `refactor`). For a squash commit with multiple distinct changes,
put additional Conventional Commit messages in the commit body; Release Please
parses each message into the release pull request.

## Breaking changes

Append `!` to type and add a `BREAKING CHANGE:` footer:

```text
feat(auth)!: rename SessionStore to Sessions

BREAKING CHANGE: SessionStore is now Sessions; rename all references.

Migration:
  // before
  store := auth.NewSessionStore(db)
  // after
  store := auth.NewSessions(db)
```

The `Migration:` footer is **required by project policy** for breaking
changes and is checked during review. Before v1.0, the `apidiff` job reports
compatibility changes without blocking the merge. Release Please builds the
changelog from Conventional Commit messages; maintainers add a migration guide
when an upgrade needs more than the commit's before/after example.

## Licensing and the Developer Certificate of Origin

Contributions are accepted under the licence of the material they touch —
`EUPL-1.2` for code, `CC-BY-SA-4.0` for prose (see [LICENSING.md](LICENSING.md)).
Every commit must carry a `Signed-off-by:` trailer certifying the
[Developer Certificate of Origin](https://developercertificate.org/):

```bash
git commit -s -m "feat(scope): subject"
```

CI rejects pull requests with unsigned commits. New Go files start with the
SPDX header (`goheader` lint enforces it):

```go
// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2
```

## CI gates

Every PR against `main` runs [`ci.yml`](.github/workflows/ci.yml). Branch
protection requires three checks: **CI success** (an aggregate — green only
if every job below it passed), **PR title (Conventional Commits)**, and
**Analyze (go)** (CodeQL's default-setup scan, a separate workflow from
`ci.yml`).

Jobs that feed the **CI success** aggregate:

- `lint` — golangci-lint on the primary root + `core/` modules, plus immutable
  Ruff over all repository Python
- `gosec` — security scan on the primary modules; root findings are uploaded
  to GitHub code scanning as SARIF
- `vuln` — fail-closed govulncheck JSON policy on the primary modules
- `test` — `go test -race -count=1` on the primary modules with merged
  coverage gated at 70%, plus 48 required Python policy regression tests
- `build` — `go build ./...` plus `go vet ./...` on the primary modules;
  immutable Clang/clang-tidy also compiles C and verifies the checked eBPF object
- `module-sweep` — four deterministic shards apply the same lint, gosec,
  govulncheck, module-tidiness, build/vet, and race-test gates to every other
  discovered module
- `markdownlint` — pinned structural lint over repository-owned public Markdown;
  generated agent context, release changelogs, HISS fixtures, and vendored
  upstream snapshots remain under their dedicated authorities
- `mkdocs` — strict documentation-site build from immutable tooling
- `allocation-budget` — measured byte/op and allocation/op ceilings for named
  hot paths
- `shellcheck` and `actionlint` — pinned full-tree shell and workflow lint
- `terraform` and `kubeconform` — locked Terraform validation plus static and
  rendered Kubernetes schema checks
- `semgrep` — blocking full-tree custom SAST and HISS fixture replay
- `reuse` — REUSE/SPDX licensing compliance (`capabilities.yaml` drift is
  covered here too, via the `TestCapabilities` test in the root suite —
  every package must be in the capability contract)
- `spectral` — OpenAPI lint; a missing example spec is an explicit successful
  no-op
- `dependency-review` — fails on high/critical advisories or a GPL/AGPL
  licence entering the tree (PR-only; explicitly skipped on other events)
- `gitleaks` — full-history secret scan
- `dco` — every human-authored commit in the PR must carry `Signed-off-by:`;
  Renovate and non-PR events are explicitly not applicable
- `apidiff` — checks every discovered Go module against the previous root
  release tag; discovery and checker errors fail the aggregate, while detected
  incompatibilities remain informational before v1.0

Two more gates run outside `ci.yml` and are not part of `ci-success`:

- **[`security-scan.yml`](.github/workflows/security-scan.yml)** — a second,
  broader Semgrep pass (public `p/golang`, `p/javascript`, `p/security-audit`
  rulesets) on push, PR, and a weekly schedule
- **Scorecard** ([`scorecard.yml`](.github/workflows/scorecard.yml)) —
  `workflow_dispatch` only; not run automatically on PRs

## Local dev

```bash
praetorctl state init --if-absent  # once per fresh checkout: seeds .workingdir/
                                    # (HISS-17 state ledger) without touching any
                                    # ledger that already exists
make dev    # air hot-reload (when implemented)
make ci     # full local CI (root module)
make verify-all  # all 23 Go modules + security, licensing, and governance
make python-lint python-test c-quality  # focused Python and C gates
make gen    # sqlc / ogen / mockery codegen
```

## Git hooks (lefthook)

Local gates run through [lefthook](https://github.com/evilmartians/lefthook)
— configuration in [`lefthook.yml`](lefthook.yml). golusoris-specific checks
are one bash script per check under [`scripts/hooks/`](scripts/hooks/);
governance checks (`context-check`, `hiss-audit`, `state-sync`,
`dedupe-cadence`, and the pre-push `audit` job) are adopted verbatim from
[cordanaLLM/praetor](https://github.com/cordanaLLM/praetor)'s `praetorctl
adopt` scaffold — see the comment header in `lefthook.yml` for which checks
stayed on the golusoris implementation and why (HISS-19: one behavior, one
implementation). Install once per clone:

```bash
make tools-bootstrap     # installs every repository-pinned Go development tool
lefthook install          # writes .git/hooks/{pre-commit,commit-msg,post-commit,pre-push}
```

| Hook | Runs |
| --- | --- |
| `pre-commit` (parallel, staged `*.go` only) | `gofumpt -l`, `gci list`, `golangci-lint run --config .golangci.yml` and `go vet` on the packages of the staged files; `praetorctl compile-context --verify`, `praetorctl audit`, `reuse lint`, `gitleaks git --staged` |
| `commit-msg` | Conventional Commits subject (`<type>(<scope>): <description>`) and the DCO `Signed-off-by:` trailer |
| `post-commit` | `praetorctl state sync .`, `praetorctl dedupe cadence --threshold=20 --record .` |
| `pre-push` | all primary-module build/vet + `go test -short` (no `-race`); `GO_MODULE_GROUP=primary scripts/ci/go-modules.sh vuln`; `praetorctl audit` |
| `agent-checkpoint-tool` / `agent-checkpoint-stop` | Bounded checkpoint evaluator (`.config/lefthook/scripts/checkpoint.py`); disabled until a reviewed `.config/agent/checkpoint.json` is added locally — not part of this adoption |

`go test -short` skips the testcontainers-backed helpers in `testutil/`
(`testutil/pg` and its dependents) — those need a Docker daemon, which a
pre-push hook cannot assume is available (#492). The full race suite,
including the container-backed packages, runs in CI (`ci.yml`'s `test` job).

Two governance jobs from praetor's scaffold are configured but currently held
back, commented out in [`lefthook.yml`](lefthook.yml)'s `pre-push` block:
`flavor-audit` (`praetorctl flavor audit .`) and `gate` (`praetorctl gate run
--path=.`, whose fourth of six stages is the flavor audit). Praetor classifies this
repository as a `go-service` flavor and demands a `Dockerfile`, an `on:`-less
`security.yml`, and duplicate lint/gosec stubs that don't fit a library
(tracked upstream as
[cordanaLLM/praetor#36](https://github.com/cordanaLLM/praetor/issues/36)).
Re-enable both once the flavor classification is fixed — `praetorctl flavor
audit .` and `praetorctl gate run --path=.` must both exit `0` on `main`
first. The complete gate also includes isolated race tests and a signed Exit-0
receipt after flavor conformance.

Formatting, lint, REUSE, gitleaks, and pre-push govulncheck hooks emit an
install hint and skip when their optional local binary is absent. Praetor
governance hooks fail closed when neither `praetorctl` nor the in-repository
legacy source is available. The hooks never require Python outside the
checkpoint lifecycle jobs. CI (`make verify-all`) remains the authoritative
gate, so an allowed local skip is still enforced on the PR.
An installed but mismatched gofumpt fails with the exact
`make tools-bootstrap` remediation; the shared pin lives in
`tools/tool-versions.env`.

Dry-run without committing:

```bash
lefthook run pre-commit --no-auto-install                      # staged files
lefthook run pre-commit --no-auto-install --file path/to/x.go  # a specific file
lefthook run commit-msg --no-auto-install .git/COMMIT_EDITMSG
```

## Releasing (maintainers)

Releases are prepared by [release-please](https://github.com/googleapis/release-please)
in manifest mode ([`release-please-config.json`](release-please-config.json),
[`.release-please-manifest.json`](.release-please-manifest.json)), running on
every push to `main` via [`release-please.yml`](.github/workflows/release-please.yml).
It tracks two components — the root `golusoris` package and the `core`
sub-module — and keeps a single release PR up to date from Conventional
Commits history, accumulating both components' version bumps and changelog
entries.

The action runs with `skip-github-release: true`, so release-please never
tags or publishes a release by itself here; it only opens/updates the PR. A
maintainer:

1. Reviews and merges the release PR.
2. Pushes the tags explicitly on the merge commit: `vX.Y.Z` for the root
   module on every release, and `core/vX.Y.Z` only when the `core` sub-module
   changed in that release (its version is in the manifest; v0.10.1 had no
   core tag). Use annotated tags and push them by name, for example
   `git tag -a v0.10.2 <merge> -m v0.10.2 && git push origin v0.10.2`.
   release-please labels its PR `autorelease: pending` on open; because the
   action runs with `skip-github-release: true` it never relabels, so the
   maintainer swaps the label to `autorelease: tagged` after pushing the tags.
   release-please refuses to open the next release PR while a merged one is
   still labelled pending.
3. Pushing the root `vX.Y.Z` tag triggers two workflows:
   - [`release.yml`](.github/workflows/release.yml) — goreleaser builds
     multi-arch archives for `cmd/golusoris` and `cmd/golusoris-mcp`,
     checksums, a keyless cosign signature bundle, per-archive SPDX SBOMs,
     and build provenance (`actions/attest-build-provenance`), then
     publishes the GitHub release.
   - [`sbom.yml`](.github/workflows/sbom.yml) — attempts source-tree SPDX and
     CycloneDX attestations and workflow artifacts. The v0.12.0 run failed
     during Rekor publication while the release workflow and its per-archive
     SBOMs succeeded; confirm this workflow on the new tag before claiming its
     source-tree attestations are available.

GitHub immutable releases are enabled, so a published release's assets
cannot be amended — a mistake needs a new patch release, not a re-run.
