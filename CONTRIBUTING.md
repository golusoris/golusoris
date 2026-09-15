# Contributing

## Conventional commits

All commits and PR titles MUST follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

[optional body]

[optional footer(s)]
```

Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`.

Scopes are subpackage names: `feat(jobs):`, `fix(db/pgx):`, `chore(tools):`.

## Breaking changes

Append `!` to type and add a `BREAKING CHANGE:` footer:

```
feat(auth)!: rename SessionStore to Sessions

BREAKING CHANGE: SessionStore is now Sessions; rename all references.

Migration:
  // before
  store := auth.NewSessionStore(db)
  // after
  store := auth.NewSessions(db)
```

The `Migration:` footer is **required** for breaking changes. CI fails without it. The footer is auto-stitched into `docs/migrations/vX.Y.Z.md`.

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

- `lint` — golangci-lint, run separately against the root module and `core/`
- `gosec` — security scan; findings are uploaded to GitHub code scanning as SARIF
- `vuln` — govulncheck, root and `core/`
- `test` — `go test -race -count=1`, root + `core/` merged into one coverage
  profile; fails if total coverage drops below 70%
- `build` — `go build ./...` (root and `core/`, plus `go vet` in `core/`)
- `reuse` — REUSE/SPDX licensing compliance (`capabilities.yaml` drift is
  covered here too, via the `TestCapabilities` test in the root suite —
  every package must be in the capability contract)

Jobs that run on every PR but are not required for merge (informational or
advisory, so a red run here does not block):

- `apidiff` — API compatibility vs. the previous tag; pre-1.0 SemVer permits
  breaking changes, so this is informational only
- `spectral` — OpenAPI lint, only when `examples/full/openapi.yaml` exists
- `dependency-review` — fails on high/critical advisories or a GPL/AGPL
  licence entering the tree (PR-only)
- `gitleaks` — full-history secret scan
- `semgrep` ("Custom SAST") — the project's own `.semgrep.yml` rules;
  `continue-on-error`, so a new rule can land without blocking unrelated PRs
  while it soaks
- `changelog` — validates `changelog.d/` fragments render against the
  `[Unreleased]` block in `CHANGELOG.md` (PR-only)
- `dco` — every commit in the PR must carry `Signed-off-by:` (PR-only; the
  `commit-msg` hook below catches this earlier, locally)

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
make verify-all  # root + core + governance gates (what CI runs)
make gen    # sqlc / ogen / mockery codegen
```

## Git hooks (lefthook)

Local gates run through [lefthook](https://github.com/evilmartians/lefthook)
— configuration in [`lefthook.yml`](lefthook.yml). golusoris-specific checks
are one bash script per check under [`scripts/hooks/`](scripts/hooks/);
governance checks (`context-check`, `hiss-audit`, `state-sync`,
`dedupe-cadence`, and the pre-push `audit` job) are adopted verbatim from
[cordanaLLM/praetor](https://github.com/cordanaLLM/praetor)'s `standardsctl
adopt` scaffold — see the comment header in `lefthook.yml` for which checks
stayed on the golusoris implementation and why (HISS-19: one behavior, one
implementation). Install once per clone:

```bash
go install github.com/evilmartians/lefthook@latest
lefthook install          # writes .git/hooks/{pre-commit,commit-msg,post-commit,pre-push}
```

| Hook | Runs |
| --- | --- |
| `pre-commit` (parallel, staged `*.go` only) | `gofumpt -l`, `gci list`, `golangci-lint run --config .golangci.yml` and `go vet` on the packages of the staged files; `praetorctl compile-context --verify`, `praetorctl audit`, `reuse lint`, `gitleaks git --staged` |
| `commit-msg` | Conventional Commits subject (`<type>(<scope>): <description>`) and the DCO `Signed-off-by:` trailer |
| `post-commit` | `praetorctl state sync .`, `praetorctl dedupe cadence --threshold=20 --record .` |
| `pre-push` | `go build ./...` + `go test -short ./...` (no `-race`) in the root and `core/` modules; `govulncheck ./...`; `praetorctl audit` |
| `agent-checkpoint-tool` / `agent-checkpoint-stop` | Bounded checkpoint evaluator (`.config/lefthook/scripts/checkpoint.py`); disabled until a reviewed `.config/agent/checkpoint.json` is added locally — not part of this adoption |

`go test -short` skips the testcontainers-backed helpers in `testutil/`
(`testutil/pg` and its dependents) — those need a Docker daemon, which a
pre-push hook cannot assume is available (#492). The full race suite,
including the container-backed packages, runs in CI (`ci.yml`'s `test` job).

Two governance jobs from praetor's scaffold are configured but currently held
back, commented out in [`lefthook.yml`](lefthook.yml)'s `pre-push` block:
`flavor-audit` (`praetorctl flavor audit .`) and `gate` (`praetorctl gate run
--path=.`, stage 4 of which **is** the flavor audit). Praetor classifies this
repository as a `go-service` flavor and demands a `Dockerfile`, an `on:`-less
`security.yml`, and duplicate lint/gosec stubs that don't fit a library
(tracked upstream as
[cordanaLLM/praetor#36](https://github.com/cordanaLLM/praetor/issues/36)).
Re-enable both once the flavor classification is fixed — `praetorctl flavor
audit .` and `praetorctl gate run --path=.` must both exit `0` on `main`
first. `standardsctl gate run` already passes stages 1-3 (lockfiles, HISS
scan, security); only stage 4 is blocked.

A check whose tool is not on PATH (`gofumpt`, `gci`, `golangci-lint`,
`praetorctl`/`standardsctl`, `reuse`, `gitleaks`, `govulncheck`) skips with an
install hint instead of failing — the hooks never require Python outside the
checkpoint lifecycle jobs. CI (`make verify-all`) remains the authoritative
gate, so a skipped local check is still enforced on the PR.

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
     and SLSA build provenance (`actions/attest-build-provenance`), then
     publishes the GitHub release.
   - [`sbom.yml`](.github/workflows/sbom.yml) — publishes source-tree SPDX
     and CycloneDX SBOMs as GitHub attestations and workflow artifacts.

GitHub immutable releases are enabled, so a published release's assets
cannot be amended — a mistake needs a new patch release, not a re-run.
