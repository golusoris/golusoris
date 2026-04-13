# Session state — golusoris

> Persistent state across workstations and AI sessions. Updated as significant changes happen.
> Last update: 2026-04-13 (Step 2 — DB landed).

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

## Local repo state

- Branch: `main`, in sync with `origin/main`.
- **Step 1 (Skeleton + Core)** ✓ committed + pushed (commit f2bdd15).
- **Step 2 (DB)** ✓ implemented locally; not yet pushed.
  - `db/pgx/` — `*pgxpool.Pool` fx module, retry-on-start (exp backoff), slow-query tracer, koanf-driven config.
  - `db/migrate/` — golang-migrate v4 runner with pgx/v5 driver, optional auto-up on fx Start, supports file:// and embed.FS sources.
  - `db/sqlc/` — `WithTx` helper + `MapError` (pgx errors → golusoris error codes).
  - `testutil/pg/` — testcontainers-go Postgres helper (`Start` returns pool, `DSN` returns connection string). Docker required.
  - `tools/sqlc.yaml.fragment` — shared sqlc v2 config template.
  - `golusoris.DB` umbrella module added.
  - `config.Unmarshal` extended with mapstructure decode hooks (time.Duration + comma-sep slices). Backwards compatible.
  - CI workflow tweaked: `docker info` precheck + 10m test timeout for testcontainers cold starts.
- Local sweep clean: `go test -race ./...` ✓ · `golangci-lint` 0 issues · `gosec` clean · `govulncheck` clean.

### Decisions made during Step 2

| Topic | Choice | Why |
|---|---|---|
| Step 2 scope | pgx + migrate + sqlc + testutil/pg (db/bun deferred) | Cleanest increment per §10. db/bun adds surface w/o demand. |
| testutil/pg fallback | Hard-fail (no t.Skip) when Docker missing | "CI without Docker is a CI bug" — matches user instruction. |
| Connect retry | Exp backoff, 10 attempts × 50ms→5s, koanf-tunable | Matches typical k8s init-container pattern. |
| Slow-query threshold | 200ms default, koanf-tunable, 0 disables | Reasonable OLTP sweet spot. |
| sqlc.yaml | Shared fragment in tools/, not generated code | sqlc is a tool; framework provides config + runtime helpers only. |

## Pending action items

- [x] Transfer + rename 4 apps to `golusoris/app-*` ✓ 2026-04-13
- [x] Apply PR merge settings + security defaults to each app ✓ 2026-04-13
- [x] First commit + push of `golusoris/golusoris` framework code ✓ 2026-04-13
- [x] Apply per-repo branch protection on `golusoris/golusoris` main ✓ 2026-04-13
- [x] Update `golusoris/.github/profile/README.md` to proper org overview ✓ 2026-04-13
- [x] Add CI workflow (lint + test + vuln + build) to `golusoris/golusoris` ✓ 2026-04-13
- [x] Add auto-assign workflow to `golusoris/golusoris` ✓ 2026-04-13
- [x] Apply branch protection to `goenvoy` + 4 app repos ✓ 2026-04-13 (note: app-arca / app-revenge default branch is `develop`, not `main`)
- [x] Org profile README rewritten as proper org overview ✓ 2026-04-13
- [ ] Pin `golusoris/golusoris` and `golusoris/goenvoy` on org page — UI-only: <https://github.com/orgs/golusoris>
- [ ] Upload org avatar — UI-only: <https://github.com/organizations/golusoris/settings/profile>
- [ ] Add `.github/workflows/labels.yml` in each repo to sync labels from `golusoris/.github/labels.yml`
- [ ] GitHub Sponsors enrollment (if desired; Ko-fi already set)
- [ ] Org-wide ruleset would require Team plan ($4/mo) — currently using per-repo classic branch protection (free, applied after first push)

## Session log (recent)

- 2026-04-13: initial scaffold pushed, CI green, branch protection applied, apps moved to `golusoris/app-*`, Ko-fi button added to framework README + org profile.
- Go toolchain bumped to 1.26.2 across go.mod + CI.
- Specialty modules (web3, gonum, ebiten, GPIO, gopter, pact, DOCX, SMTP server, DNS server, etc.) pulled from "out of scope" into §3.16 / §3.16b of PLAN.md. Heavy/CGO ones will live as in-repo sub-modules with their own go.mod.
- 2026-04-13: **Step 2 — DB** landed locally. db/pgx + db/migrate + db/sqlc + testutil/pg + golusoris.DB umbrella + tools/sqlc.yaml.fragment. config.Unmarshal extended with mapstructure decode hooks. CI test job added Docker precheck + 10m timeout.
- 2026-04-13: **Step 3 — HTTPX base** landed in 3 commits:
  - 3a (`feat(httpx)`): httpx/server (slow-loris + body limits + graceful shutdown), httpx/router (chi v5), httpx/middleware (RequestID, Recover, Logger, OTel, SecureHeaders, TrustProxy, Compress, ETag, Chain). golusoris.HTTP umbrella.
  - 3b (`feat(httpx/client)`): outbound client — breaker(outer) → retry → otelhttp → stdlib.
  - 3c (`feat(ogenkit, apidocs)`): ogenkit (ErrorHandler, SlogMiddleware, RecoverMiddleware for ogen), apidocs (Scalar UI at /docs with embedded bundle, MCP JSON-RPC at /mcp with tools/list + tools/call from OpenAPI, openapi.yaml|json). tagliatelle exempt for apidocs/ (MCP protocol fields are camelCase by spec).
- 2026-04-13: **Step 4 — HTTPX extras** landed in 3 commits:
  - 4a (`feat(httpx)` tiny wrappers): form (go-playground/form/v4, gerr.CodeBadRequest on decode fail), htmx (HX-* header constants + helpers), vite (manifest.json → hashed URLs + transitive CSS), static (embed fs.FS + ETag + cache-control), static/hashfs (benbjohnson/hashfs).
  - 4b (`feat(httpx)` security middleware): cors (rs/cors, deny-default), csrf (gorilla/csrf double-submit, no-secret=no-op), ratelimit (ulule/limiter/v3 memory store + X-RateLimit-* headers), geofence (maxminddb, app-supplied mmdb, allow/deny ISO-3166-1).
  - 4c (`feat(httpx)` ws + autotls): ws (coder/websocket thin wrapper with same-origin Accept + in-process Broadcaster[T]), autotls (pluggable Provider interface + autocert + certmagic sub-modules). httpx/server now picks up optional *tls.Config via fx and wraps the listener when present.

### Decisions made during Step 3

| Topic | Choice | Why |
|---|---|---|
| Commit shape | 3 commits (3a/3b/3c) | Per user; eases review + reverts. |
| OTel middleware | accept trace.TracerProvider, fall back to otel global no-op | Decouples httpx from a future otel/ package. |
| apidocs scope | Scalar + MCP both now | Per user; MCP coverage is a pragmatic subset (initialize, tools/list, tools/call — stateless HTTP). |
| Scalar delivery | embed @scalar/api-reference@1.25.52 bundle via go:embed | Airgap-safe; pinned = reproducible. Refresh via `make scalar-update`. |
| koanf env transform | live with single-underscore limit; nest struct fields to single words | Avoided breaking the env-provider contract by grouping multi-word concepts under sub-structs (e.g. `http.timeouts.read`). |

## How to use this file

- `.workingdir/PLAN.md` is the architectural source of truth (decisions log + module catalog).
- `.workingdir/STATE.md` (this file) is the operational state — what exists, what's pending, what was configured where.
- Both are committed to the framework repo so any clone on any workstation gets the full context.
- `.workingdir/` should NOT be added to `.gitignore`.
