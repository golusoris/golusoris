# Session state — golusoris

> Persistent state across workstations and AI sessions. Updated as significant changes happen.
> Last update: 2026-04-13.

## Naming conventions (Option B)

| Kind | Path | Example |
|---|---|---|
| Framework (namesake) | `golusoris/golusoris` | the framework |
| Library | `golusoris/<name>` | `golusoris/goenvoy` |
| App | `golusoris/app-<name>` | `golusoris/app-lurkarr` |
| Tool/CLI (future) | `golusoris/cmd-<name>` (proposed) | — |

## Repos (current state)

### `golusoris/` org
| Repo | Status | Notes |
|---|---|---|
| `golusoris/golusoris` | created (empty), public, 14 topics | local scaffold ready, awaiting first push |
| `golusoris/goenvoy` | transferred from lusoris ✓ | library; FUNDING + security defaults inherited |
| `golusoris/.github` | populated ✓ | FUNDING.yml (Ko-fi: lusoris) + profile/README.md + labels.yml + sync-labels.yml workflow |
| `golusoris/app-lurkarr` | transferred + renamed ✓ | redirect from `lusoris/Lurkarr` active |
| `golusoris/app-subdo` | transferred + renamed ✓ | redirect from `lusoris/subdo` active |
| `golusoris/app-revenge` | transferred + renamed ✓ | redirect from `lusoris/revenge` active |
| `golusoris/app-arca` | transferred + renamed ✓ | redirect from `lusoris/arca` active |

### `lusoris/` user
| Repo | Disposition |
|---|---|
| `lusoris/.github` | KEEP (covers any future personal repos) |
| (apps all transferred away) | redirects from old paths still functional via GitHub's automatic forwarding |

## Org settings — `golusoris/`

- Display name: `golusoris`
- Description: "Composable Go framework — opt-in fx modules for production backends."
- Blog/website: https://github.com/golusoris/golusoris
- Default branch: `main`
- Default repo permission: `read`
- 2FA required: ✅ enabled
- New-repo security defaults: Dependabot alerts ✓, security updates ✓, dependency graph ✓, secret scanning ✓, push protection ✓
- Workflow permissions: `default_workflow_permissions=read`, `can_approve_pull_request_reviews=false`
- Actions allowlist: `selected` — github-owned ✓, verified ✓, plus pattern allowlist (cosign, syft/anchore, goreleaser, golangci, docker buildx, release-please, digestabot, slsa-framework, etc.)

## Repo settings (new repos in `golusoris/` org)

| Setting | Value |
|---|---|
| `allow_squash_merge` | true |
| `allow_rebase_merge` | true |
| `allow_merge_commit` | false |
| `delete_branch_on_merge` | true |
| `squash_merge_commit_title` | `PR_TITLE` |
| `squash_merge_commit_message` | `PR_BODY` |

(Org has no central control on free plan — apply per-repo as new repos are created.)

## Local repo state — `c:\Users\ms\dev\golusoris\`

- Git initialized on `main` branch, no commits yet.
- All Step 1 files written + tested locally (`go test ./...` clean).
- Modules implemented: config, log, errors, clock, id, validate, i18n, crypto + top-level `golusoris.go`.
- Tools configs in `tools/`: Makefile.shared, golangci.yml, mockery.yaml, air.toml, Dockerfile.template, Dockerfile.media.template, docker-compose.dev/prod.yml, .goreleaser.yml, .pre-commit-config.yaml, spectral.yaml.
- Foundation docs: README.md (with badges), LICENSE (MIT), CONTRIBUTING.md, SECURITY.md, AGENTS.md, CLAUDE.md, doc.go (top-level overview).
- Test files: every module has at least one *_test.go + Example* tests.
- **Not yet pushed** to `golusoris/golusoris` — awaiting user "go" on commit/push.

## Pending action items

- [ ] Transfer + rename 4 apps: `lusoris/{Lurkarr,subdo,revenge,arca}` → `golusoris/app-{lurkarr,subdo,revenge,arca}` (lower-case the name in transfers — `Lurkarr` → `app-lurkarr`).
- [ ] After transfers: apply PR merge settings + verify security defaults on each.
- [ ] Pin `golusoris/golusoris` and `golusoris/goenvoy` on org page (UI-only — https://github.com/orgs/golusoris ).
- [ ] Upload org avatar (UI-only — https://github.com/organizations/golusoris/settings/profile ).
- [ ] Upload Ko-fi handle is set; `github_sponsors` not configured (would need GitHub Sponsors enrollment if desired).
- [ ] Org-wide ruleset for branch protection: needs Team plan ($4/mo). Use per-repo classic branch protection (free for public repos) instead, applied after first push.
- [ ] First commit + push of `golusoris/golusoris` framework code (awaiting user "go").
- [ ] Update `golusoris/.github/profile/README.md` to list app-* repos once transfers done.

## How to use this file

- `.workingdir/PLAN.md` is the architectural source of truth (decisions log + module catalog).
- `.workingdir/STATE.md` (this file) is the operational state — what exists, what's pending, what was configured where.
- Both are committed to the framework repo so any clone on any workstation gets the full context.
- `.workingdir/` should NOT be added to `.gitignore`.
