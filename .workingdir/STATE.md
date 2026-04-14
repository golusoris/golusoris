# Session state — golusoris

> Persistent state across workstations and AI sessions. Updated as significant changes happen.
> Last update: 2026-04-13 (Step 6.5 — runtime-agnostic + leader refactor + systemd landed).

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
| Step 2 scope | pgx + migrate + sqlc + testutil/pg (db/bun deferred) | Cleanest increment per §11. db/bun adds surface w/o demand. |
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

- 2026-04-14: **Step 10b — notify providers (mailgun + sendgrid + telegram + teams + twilio + webpush)** landed:
  - `notify/mailgun/` — form-encoded POST to `api.mailgun.net/v3/{domain}/messages` with basic auth `api:<key>`. Metadata → `v:<key>` user-variables. EU region via `Endpoint: mailgun.EURegionEndpoint`.
  - `notify/sendgrid/` — JSON POST to `api.sendgrid.com/v3/mail/send` with Bearer token. Single personalization bundle (To/CC/BCC), `Content` emitted text/plain before text/html per spec, `Metadata` → `custom_args`.
  - `notify/telegram/` — JSON POST to `api.telegram.org/bot{token}/sendMessage`. Chat resolution: `msg.To[0]` overrides `Options.ChatID`. `ParseModeNone/HTML/MarkdownV2` constants.
  - `notify/teams/` — MessageCard JSON POST to Teams incoming webhook (legacy connector or Power Automate Workflow URL). `@context/@type/themeColor` wire fields — added `notify/teams/` to tagliatelle path exclusion.
  - `notify/twilio/` — form-encoded POST per recipient to `api.twilio.com/2010-04-01/Accounts/{SID}/Messages.json` with basic auth `{SID}:{token}`. Mutually exclusive `From` vs `MessagingServiceSID` validated at construction.
  - `notify/webpush/` — Web Push Protocol (RFC 8030 + VAPID RFC 8292) via `SherClockHolmes/webpush-go` v1.4.0 (lightweight, only `x/crypto` transitive). Helpers: `NewVAPIDKeys()`, `EncodeSubscription()`. Subscription JSON carried via `msg.Metadata["subscription"]`.
  - Deferred (separate commit): `notify/fcm/` (Google OAuth2 JWT flow), `notify/apns2/` (Apple ES256 JWT or TLS client cert), `notify/ses/` (AWS SigV4).
  - 0 lint · race-green across `./notify/...` (10 packages).
- 2026-04-14: **Step 10a — notify providers (Resend + Postmark)** landed:
  - `notify/resend/` — raw-HTTP sender (no SDK). POSTs JSON to
    `api.resend.com/emails` with `Authorization: Bearer <key>`.
    `Options{APIKey, From, ReplyTo, Endpoint, HTTPClient}`. Maps
    `notify.Message.Metadata` → Resend tags. Endpoint override for EU
    region (`api.eu.resend.com`) + tests.
  - `notify/postmark/` — raw-HTTP sender (no SDK). POSTs JSON to
    `api.postmarkapp.com/email` with `X-Postmark-Server-Token`.
    `Options{ServerToken, From, ReplyTo, MessageStream, Endpoint, HTTPClient}`.
    Joins `To/Cc/Bcc` with comma per Postmark wire format.
  - Both senders implement the same `notify.Sender` iface so the
    Notifier (first-success / Multi fan-out) keeps a stable contract
    across SMTP / Discord / Slack / Resend / Postmark.
  - `tools/golangci.yml`: added `notify/postmark/` to tagliatelle path
    exclusion (Postmark wire format is PascalCase by spec).
  - 0 lint · race-green across `./notify/...`.
- 2026-04-14: **PLAN.md round-12** added per-provider notify subpackage
  list (twilio/fcm/apns2/mailgun/sendgrid/telegram/teams/webpush) +
  `db/cdc/` for logical replication via jackc/pglogrepl.
- 2026-04-14: **Lint expansion** landed: golangci-lint v2 now enforces 30+ linters across the framework (forbidigo, gosec, contextcheck, wrapcheck, govet enable-all, recvcheck, unparam, usetesting, prealloc, musttag, sloglint, tagliatelle, revive, paralleltest, testifylint, depguard, gomodguard, perfsprint, importas, …). Backlog of 241 issues fixed to 0:
  - clockwork.Clock injection added to `auth/lockout`, `auth/magiclink`, `idempotency` `MemoryStore`s (via `NewMemoryStoreWithClock`).
  - `auth/passkeys.VerifyTOTP` split into `VerifyTOTP` (wall-clock, justified `//nolint:forbidigo`) + `VerifyTOTPAt(at time.Time)` for tests.
  - `auth/oauth2server` + `notify/unsub` add `http.MaxBytesReader` form-body limits + XSS-escaped responses.
  - `db/geo.Point` migrated to pointer receivers (recvcheck).
  - `pubsub/kafka.NewRecord` no longer stamps timestamps (broker-assigned).
  - `cmd/golusoris.bumpGolusoris` uses `exec.CommandContext` (noctx).
  - sloglint *Context variants applied to `notify`, `db/pgx`, `outbox/drainer`, `realtime/sse`, `systemd`.
  - tagliatelle path exclusions for RFC-mandated wire formats: SCIM 2.0 (camelCase), reCAPTCHA (kebab-case), OAuth `access_token` (gosec G117).
  - musttag excluded for `_test.go` (ad-hoc fixtures don't need json tags).
  - wrapcheck `extra-ignore-sigs` expanded with stdlib leaf returns we forward verbatim.
  - New senders: `notify/discord` + `notify/slack` (raw HTTP webhook, no SDK).
  - 0 lint · 0 govulncheck (3 transitive, unreachable) · race-green across `./...`.
- 2026-04-14: **Step 9 cont. — auth completion** landed (10 packages, undeferred per user direction):
  - `auth/lockout/` — per-identity brute-force lockout with `Service.RegisterFail/Reset/IsLocked`. `Options{MaxFails, Window, Cooldown}`. Pluggable `Store`; `MemoryStore` uses clockwork.Clock. `errors.As` on missing-state lookup.
  - `auth/captcha/` — `Verifier` interface across Cloudflare Turnstile, hCaptcha, Google reCAPTCHA. Same wire shape (POST form `secret/response/remoteip` → JSON `{success}`) — code-share via `httpVerify`. Test uses `rewriteTransport` to point at `httptest.Server`.
  - `auth/policy/` — password policy: zxcvbn score (nbutton23/zxcvbn-go) + HIBP k-anonymity check (5-char SHA1 prefix). `gerr.Validation` for failures. `//nolint:gosec` justification on SHA1 (HIBP API contract).
  - `auth/recovery/` — single-use recovery codes + reset tokens. `Service.IssueCodes/VerifyCode/IssueResetToken/VerifyResetToken`. HMAC-SHA256 hashing; raw revealed only at issuance. `subtle.ConstantTimeCompare`.
  - `auth/magiclink/` — passwordless email links. `Link{Email, Hash, ExpiresAt, UsedAt}`. Default TTL 15min. `MemoryStore`.
  - `auth/linking/` — multi-IdP identity linking. `Identity{Provider, Subject, UserID, Email}`. Conflict error when (provider,subject) already bound to a different user. Store keyed by `provider+"|"+subject`.
  - `auth/impersonate/` — admin-as-user with banner. `X-Impersonating` header, `?exit_impersonation=1` revert, `Principal{Current, Original}` on context. `Begin` rejects nesting (`gerr.Forbidden`). `OnImpersonate/OnExit` audit hooks.
  - `auth/passkeys/` — WebAuthn (go-webauthn/webauthn) + TOTP (pquerna/otp). `BeginRegistration/FinishRegistration/BeginLogin/FinishLogin/ProvisionTOTP/VerifyTOTP`. Default TOTP: SHA1, 30s, 6 digits, ±1 period skew.
  - `auth/oauth2server/` — minimal OAuth 2.1 issuer, authorization-code+PKCE only (S256/plain). `Server.Routes()` exposes `/authorize` + `/token`. Issues JWT access tokens via `*jwt.Signer`. Codes single-use, 60s TTL. `MemoryClientStore`+`MemoryCodeStore`. **Decision:** wrote ~350 LOC from scratch instead of pulling `ory/fosite` (heavy transitive deps: jaeger, zipkin, ini, otlptrace exporters).
  - `auth/scim/` — SCIM 2.0 (RFC 7643/7644) `/Users` `/Groups` CRUD. Schema URIs `urn:ietf:params:scim:schemas:core:2.0:{User,Group}`. PATCH not implemented (PUT replaces). Pluggable `Store`; `ErrNotFound`→404.
  - All packages: `AGENTS.md` + table-driven tests with `stretchr/testify/require`. `go test -race` green.
  - Drive-by: fixed pre-existing errcheck issues in `config/example_test.go`, `httpx/client/client_test.go`, `notify/smtp.go`, `notify/unsub/unsub.go`. Repo lint: 0 issues across `./...`.
- 2026-04-13: initial scaffold pushed, CI green, branch protection applied, apps moved to `golusoris/app-*`, Ko-fi button added to framework README + org profile.
- Go toolchain bumped to 1.26.2 across go.mod + CI.
- Specialty modules (web3, gonum, ebiten, GPIO, gopter, pact, DOCX, SMTP server, DNS server, etc.) pulled from "out of scope" into §4.16 / §4.16b of PLAN.md. Heavy/CGO ones will live as in-repo sub-modules with their own go.mod.
- 2026-04-13: **Step 2 — DB** landed locally. db/pgx + db/migrate + db/sqlc + testutil/pg + golusoris.DB umbrella + tools/sqlc.yaml.fragment. config.Unmarshal extended with mapstructure decode hooks. CI test job added Docker precheck + 10m timeout.
- 2026-04-13: **Step 3 — HTTPX base** landed in 3 commits:
  - 3a (`feat(httpx)`): httpx/server (slow-loris + body limits + graceful shutdown), httpx/router (chi v5), httpx/middleware (RequestID, Recover, Logger, OTel, SecureHeaders, TrustProxy, Compress, ETag, Chain). golusoris.HTTP umbrella.
  - 3b (`feat(httpx/client)`): outbound client — breaker(outer) → retry → otelhttp → stdlib.
  - 3c (`feat(ogenkit, apidocs)`): ogenkit (ErrorHandler, SlogMiddleware, RecoverMiddleware for ogen), apidocs (Scalar UI at /docs with embedded bundle, MCP JSON-RPC at /mcp with tools/list + tools/call from OpenAPI, openapi.yaml|json). tagliatelle exempt for apidocs/ (MCP protocol fields are camelCase by spec).
- 2026-04-13: **Step 4 — HTTPX extras** landed in 3 commits:
  - 4a (`feat(httpx)` tiny wrappers): form (go-playground/form/v4, gerr.CodeBadRequest on decode fail), htmx (HX-* header constants + helpers), vite (manifest.json → hashed URLs + transitive CSS), static (embed fs.FS + ETag + cache-control), static/hashfs (benbjohnson/hashfs).
  - 4b (`feat(httpx)` security middleware): cors (rs/cors, deny-default), csrf (gorilla/csrf double-submit, no-secret=no-op), ratelimit (ulule/limiter/v3 memory store + X-RateLimit-* headers), geofence (maxminddb, app-supplied mmdb, allow/deny ISO-3166-1).
  - 4c (`feat(httpx)` ws + autotls): ws (coder/websocket thin wrapper with same-origin Accept + in-process Broadcaster[T]), autotls (pluggable Provider interface + autocert + certmagic sub-modules). httpx/server now picks up optional *tls.Config via fx and wraps the listener when present.
- 2026-04-13: **PLAN §2 Principles & standards promoted to top-level** — Power of 10 (Go-adapted) + SEI CERT for Go + Google Go Style Guide + C4 + ADRs + SLSA L3 + OWASP ASVS L2 + NIST SSDF + EU CRA + NIS2 + BSI IT-Grundschutz + BSI C5 + UK NCSC + ENISA + GDPR + EU AI Act + RFC 9457 Problem Details + OTel SemConv v1.26 + Conventional Commits + Keep a Changelog + SemVer 2.0 + Trunk-Based Dev + EditorConfig + gofumpt/gci/golines + Twelve-Factor + CNCF cloud-native + OCI Image Spec + testing standards. Sections §2-§14 renumbered (old §2-§13). CLAUDE.md gained a Power-of-10 quick-reference section + "keep docs in sync" rule.
- 2026-04-13: **Step 6a — k8s/podinfo + k8s/health** landed: extended `statuspage.Check` with `Tags []string` + `RunTagged(ctx, tag)` so a single Registry powers `/livez` `/readyz` `/startupz` `/status`. `k8s/podinfo` reads downward-API env vars (POD_NAME/NAMESPACE/IP/NODE_NAME/SERVICE_ACCOUNT/CONTAINER_NAME/CONTAINER_IMAGE) + IsInCluster() helper. `k8s/health` ships TagLiveness/Readiness/Startup constants + per-tag handlers; default response is `ok\n` / `not ok\n`, `?verbose=1` returns JSON.
- 2026-04-13: **Step 6b — k8s/metrics/prom + k8s/leader** landed: prometheus/client_golang `/metrics` endpoint with Go runtime + process collectors + `app_check_status` / `app_check_latency_seconds` gauges per registered statuspage check (refresh via `Registry.OnRun` hook). `k8s/leader` wraps client-go's leader-election with the Lease resource lock; defaults match client-go controller timings (15s/10s/2s); supplies `Module(callbacks)` for fx wiring. statuspage gained `RunHook` + `OnRun(fn)` so subscribers (prom, future OTel meter) snapshot results.
- 2026-04-13: **Step 6.5 — runtime-agnostic + leader refactor + systemd** landed in 3 commits:
  - 6.5a (`feat(container)`): `container/runtime/` — unified Info across k8s/docker/podman/systemd/bare. Detection order k8s → podman → docker → systemd → bare. Reads SA-token file, `/.dockerenv`, `/run/.containerenv`, `NOTIFY_SOCKET`, `INVOCATION_ID`, `/proc/self/cgroup` for the 64-char container ID. Replaces k8s/podinfo as primary for new code (k8s/podinfo kept as k8s-only view).
  - 6.5b (`refactor(leader)`): promoted `leader/` to top-level with pluggable backends. Moved `k8s/leader` → `leader/k8s` (client-go Lease). Added `leader/pg` using `pg_try_advisory_lock` — session-held, auto-releases on crash, no TTL tuning. Real-pg integration test proves two-replicas-one-leader. `leader.Callbacks` shared across backends.
  - 6.5c (`feat(systemd)` + docker examples): `systemd/` — sd_notify READY=1 on Start, STOPPING=1 on Stop, WATCHDOG=1 at `WATCHDOG_USEC/2` ticker. No-op when NOTIFY_SOCKET unset. Enhanced `tools/docker-compose.dev.yml` with `/livez` healthcheck + env-mapped config. `tools/Dockerfile.template` HEALTHCHECK now hits /livez. New `tools/prometheus/prometheus.yml` scrape-config example.
- 2026-04-14: **Step 24 — Polish** landed:
  - `examples/minimal/main.go` — 5-module composition: Core + DB + otel.Module + HTTP + K8s. Compiles clean.
  - `examples/full/main.go` — production-ready composition: Core + DB + OTel + HTTP + K8s + Jobs + CacheMemory + CacheRedis + AuthOIDC + authz.Module + stripe.Module. Compiles clean.
  - 0 lint issues on examples/.
- 2026-04-13: **Step 23 — AI layer** landed (docs/upstream snapshots deferred):
  - `.claude/skills/wire-fx-module.md` — full prompt for adding fx modules (config, params, lifecycle, logging, lint rules).
  - `.claude/skills/scaffold-ogen-handler.md` — ogen handler stub from operationId (interface lookup, error mapping, test wiring).
  - `.claude/skills/add-river-worker.md` — river worker (Args struct, Worker, fx registration, rivtest harness).
  - `.claude/skills/add-migration.md` — timestamped golang-migrate up/down pair (idempotency rules, verify steps).
  - `.claude/skills/bump-golusoris.md` — bump + migration guide read + codemod application.
  - `.claude/hooks/post-tool-use.md` — context auto-loading hooks + pre-commit convention summary.
  - `docs/migrations/v0.1.x.md` — initial stable API migration guide covering config pointer, clock, error wrapping.
- 2026-04-13: **Step 22 — GitHub template + reusable workflows** landed:
  - `.github/workflows/ci-go.yml` — reusable: lint (golangci) + test (race, cover) + vuln (govulncheck) + build. Inputs: go-version-file, golangci-version, lint-timeout, test-timeout, coverage-threshold.
  - `.github/workflows/release-go.yml` — reusable: GoReleaser multi-arch + GHCR push + syft SBOM + cosign keyless + attest-build-provenance. Inputs: image-name, go-version-file, goreleaser-config.
  - `.github/workflows/codeql.yml` — reusable: CodeQL Go analysis (security-extended + security-and-quality).
  - `.github/workflows/scorecard.yml` — reusable: OSSF Scorecard with SARIF upload to code-scanning.
  - `template/.github/workflows/ci.yml` — per-app stub calling ci-go.yml@main.
  - `template/.github/workflows/release.yml` — per-app stub calling release-go.yml@main.
  - `template/.github/dependabot.yml` — gomod (weekly, golusoris group) + github-actions (weekly).
  - `template/.devcontainer/devcontainer.json` — Go 1.24 devcontainer + docker-in-docker + air + golangci + govulncheck + sqlc. VSCode extensions + port forwards (8080/5432/6379/4222).
  - `template/.devcontainer/docker-compose.yml` — app + postgres 17 + redis 7 + nats 2 with env wiring.
- 2026-04-13: **Step 21 — Deploy** landed (partial — logging/terraform/pulumi/flux/argocd/multiregion deferred):
  - `deploy/helm/` — base Helm chart (Chart.yaml v0.1.0, values.yaml with full defaults). Templates: Deployment (checksum annotation, downward-API env, /tmp emptyDir, read-only FS, non-root), Service, ServiceAccount, ConfigMap, Secret (stringData), HPA (autoscaling/v2), PDB (policy/v1), NetworkPolicy (Ingress+open-Egress), ServiceMonitor (monitoring.coreos.com/v1), Ingress. All features gated via values flags.
  - `deploy/observability/` — PrometheusRule (5 alerts: HighErrorRate, HighLatencyP99, HighMemoryUsage, GoroutineLeak, HealthCheckFailing on app_check_status). Grafana dashboard JSON (uid: golusoris-http, 3 panels: Request Rate, Error Rate, P99+P50 Latency).
- 2026-04-13: **Step 20 — CLI + MCP** landed:
  - `cmd/golusoris/` — scaffolder CLI (clikit-based). `init <name>` creates go.mod + main.go. `add <module>` prints fx wiring instructions. `bump <version>` shells `go get + go mod tidy`. Internal: `cmd/golusoris/internal/scaffold/` with init/add/bump subcommands.
  - `cmd/golusoris-mcp/` — standalone MCP JSON-RPC 2.0 server on `:8899`. Implements `initialize`, `tools/list`, `tools/call`. Exposes `golusoris_init`, `golusoris_add`, `golusoris_bump` tools. Graceful shutdown on SIGINT/SIGTERM.
  - Both binaries: `AGENTS.md` + tests (scaffold). 0 lint issues.
- 2026-04-13: **Step 19 — Testing extras** landed (partial — load/mutation deferred):
  - `testutil/fxtest/` — `New(t, opts...)` starts an fx app and registers `t.Cleanup` to stop it. `Populate` re-exports `fx.Populate` for ergonomic use alongside `New`. No new dep (go.uber.org/fx/fxtest already present).
  - `testutil/factory/` — `New(t)` returns a `*gofakeit.Faker` seeded from `t.Name()` for determinism. `Random()` for non-deterministic use. New dep: brianvoe/gofakeit/v6 v6.28.0.
  - `testutil/snapshot/` — `Match(t, value)` + `MatchJSON(t, value)` backed by gkampitakis/go-snaps. Snapshots in `__snapshots__/`. Update with `UPDATE_SNAPS=true`. New dep: gkampitakis/go-snaps v0.5.21.
  - All packages: `AGENTS.md` + tests. 0 lint issues.
- 2026-04-13: **Step 17 — Big stacks** landed (partial — grpc/graphql/temporal/outbox/cdc/ebpf deferred):
  - `db/clickhouse/` — fx module wrapping ClickHouse/clickhouse-go/v2 v2.45.0. `Exec`, `Query` (returns driver.Rows), `Conn()` accessor. Cluster Ping on fx Start. Config: `db.clickhouse.{addr,database,username,password,tls}`. New dep: ClickHouse/clickhouse-go/v2 v2.45.0.
  - `db/geo/` — PostGIS geometry helpers (no extra dep). `Point{Lon,Lat}` with `sql.Scanner` (hex EWKB) + `driver.Valuer` (EWKT). `BBox`. `Distance` (Haversine, metres). `RegisterTypes` placeholder for future pgx type registration.
  - `db/timescale/` — TimescaleDB helpers on top of pgx pool. `CreateHypertable` (idempotent), `SetRetention`, `EnableCompression`, `AddCompressionPolicy`. Interval formatting handles hours/days. No new dep (pgx already present).
  - All packages: `AGENTS.md` + tests. 0 lint issues.
- 2026-04-13: **Step 17 — Big stacks** landed (partial — grpc/graphql/temporal/geo/timescale/clickhouse/outbox/cdc/ebpf deferred):
  - `pubsub/kafka/` — fx module wrapping twmb/franz-go v1.20.7. `Client.Produce` (sync), `Poll` (up to N records), `Subscribe` (set topics), `CommitOffsets`, `NewRecord` helper. Cluster ping on fx Start. New dep: twmb/franz-go v1.20.7.
  - `pubsub/nats/` — fx module wrapping nats-io/nats.go v1.50.0. `Client.Publish` (core, fire-and-forget), `Subscribe` (callback), `JetStream()` (durable at-least-once via jetstream.JetStream). Error handler logs via slog. New dep: nats-io/nats.go v1.50.0.
  - Both packages: `AGENTS.md` + tests. 0 lint issues.
- 2026-04-13: **Step 18 — Misc** landed (plugin deferred):
  - `clikit/` — cobra + fx-aware CLI builder. `New`, `AddCommand`, `Execute`. `Command` factory with `WithFx` (starts fx app + Run), `WithRunHook` (one-shot fx action), `WithRunE` (plain cobra). New dep: spf13/cobra v1.10.2.
  - `clikit/tui/` — bubbletea helpers. `Run` (AltScreen + mouse), `RunInline` (no alt-screen), `Quit`. New dep: charmbracelet/bubbletea v1.3.10.
  - `selfupdate/` — GitHub release self-update via minio/selfupdate v0.6.0. `Update(ctx, Options)` fetches latest release, selects OS/arch asset, verifies SHA-256 checksum from goreleaser-style `*_checksums.txt`, replaces binary atomically. Returns `Result{Updated, LatestVersion, CurrentVersion}`.
  - All packages: `AGENTS.md` + tests. 0 lint issues.
- 2026-04-13: **Step 16 — Integrations** landed (partial — goenvoy/torrent deferred):
  - `geoip/` — `DB` wrapping `oschwald/maxminddb-golang`. `Open/Close`, `Lookup(ip)` → `Info{Country, City, Location}`, `LookupASN(ip)` → `ASN{Number, Organization}`, `CountryCode(ip)` convenience shortcut. No new dep (maxminddb-golang already in go.mod via httpx/geofence).
  - `secrets/` — Pluggable `Secret` interface. `ErrNotFound{Key}` for missing keys. Backends: `Env()` (os.Getenv), `File(dir)` (directory of secret files, path-traversal safe), `Static(map)` (for tests). No new deps — stdlib only.
  - Both packages: `AGENTS.md` + tests. 0 lint issues.
- 2026-04-13: **Step 15 — Commerce** landed (partial — subs/meter/invoice deferred):
  - `money/` — `Money{Amount int64, Currency string}` in minor units. `New`, `FromMajor`, `Add/Sub/Mul/Neg/Abs`, `MajorUnits`, `String`. `ZeroDecimalCurrencies` map (JPY, KRW, VND, …). Panics on currency mismatch. No new deps.
  - `payments/stripe/` — `Client` wrapping stripe-go/v82 new-style `stripe.Client` API. `NewCheckoutSession`, `NewPortalSession`, `CreatePaymentIntent`. fx `Module` wired from `payments.stripe.*` koanf config. New dep: `stripe/stripe-go v82.5.1`.
  - Deferred: `payments/subs/` (subscription state machine), `payments/meter/` (usage metering), `payments/invoice/` (depends on pdf/).
- 2026-04-13: **Step 14 — Search + AI** landed:
  - `search/` — `Backend` interface (`Indexer + Searcher`). `Query{Q, Fields, Filters, RawFilter, SortBy, Limit, Offset}`. `Results{Hits, Total}`. `MemorySearcher` in-memory backend (case-insensitive substring, filter by equality). Planned: typesense, meilisearch, pgfts sub-packages.
  - `ai/llm/` — `Client` interface (`Chat/Stream/Embed`). `OpenAIClient` HTTP backend compatible with OpenAI, Azure OpenAI, Ollama, Groq, Mistral, LM Studio. Options: `WithModel/WithMaxTokens/WithTemperature/WithSystem`. SSE streaming via channel of `Chunk`. No SDK dep — raw HTTP. Planned: `ai/llm/anthropic/` + `ai/llm/ollama/` sub-packages.
  - `ai/vector/` — pgvector-go v0.3.0 helpers. `From([]float32) Vector`. `SimilaritySearch(ctx, pool, SearchQuery)` with Cosine/L2/InnerProduct metrics. `RegisterTypes` to register pgvector OIDs on pgx pool. `gosec` nolint on SQL string format (table/column names are caller-controlled, not user input).
  - New dep: `pgvector/pgvector-go v0.3.0` (light, no transitive deps beyond pgx already present).
  - All packages: `AGENTS.md` + tests (search + llm tested against fake HTTP server). 0 lint issues.
- 2026-04-13: **Step 13 — Files/storage** landed (partial — CGO media/ocr/pdf deferred to separate go.mod submodules):
  - `storage/` — `Bucket` interface (Put/Get/Delete/Exists/List/URL). `LocalBucket` backend: path-traversal protected, 0o750 dirs, 0o640 files. Cloud backends (S3/GCS) are planned sub-packages.
  - `hash/` — `SHA256`, `BLAKE3`, `XX64`, `ETag` helpers. `*Reader` variants for streaming. Picks: cespare/xxhash v2 (fastest 64-bit Go hash, used by Prometheus), zeebo/blake3 (pure-Go, 3× faster than SHA-256).
  - `markdown/` — goldmark v1.8.2 with GFM extensions (tables, strikethrough, task lists, linkify), footnotes, typographer, auto-heading IDs. `Render`, `RenderString`, `RenderTo`.
  - `archive/` — mholt/archives v0.1.5. `Extract(ctx, src, destDir)` + `Create(ctx, dest, srcs)`. Zip-slip protected (library strips `../`). Supported: zip/tar/gz/bz2/xz/zst/7z/rar.
  - `httpx/rangeserve/` — stdlib `http.ServeContent` wrapper. `Handler(opener, keyFn)` + `ServeFile` + `ServeReader`. Pluggable `Opener` interface for storage backends.
  - `fs/watch/` — fsnotify-based debounced directory watcher. `Watcher.Add/Remove/Events/Close`. Configurable debounce + buffer. Drops events when channel full (non-blocking).
  - New deps: cespare/xxhash/v2, zeebo/blake3, yuin/goldmark v1.8.2 (was already present, bumped), mholt/archives v0.1.5, fsnotify/fsnotify.
  - All packages: `AGENTS.md` + tests. 0 lint issues.
  - **Deferred** (CGO, need separate go.mod): `media/av` (go-astiav/FFmpeg), `media/img` (govips/libvips), `media/cv` (gocv/OpenCV), `ocr/` (gosseract/Tesseract), `pdf/` (chromedp).
- 2026-04-13: **Step 12 — SaaS primitives** landed:
  - `page/` — Typed cursor-based (`NewCursorPage`, `EncodeCursor/DecodeCursor`) and offset-based (`NewOffsetPage`, `HasPrev/HasNext`) pagination. Pure utility, no fx.
  - `audit/` — Append-only structured audit log. `Event{Actor, Action, Target, TenantID, Diff, Metadata}`. `Diff` = `map[string]FieldChange{Before, After}`. `Logger` auto-assigns ID + CreatedAt via injected `clock.Clock`. `MemoryStore` for tests. `Filter` supports Actor/Action/Target/TenantID/time bounds/Limit.
  - `tenancy/` — Multi-tenant HTTP middleware. `ExtractFunc` → `Store.FindByID` → context. Ships `HeaderExtractor` + `SubdomainExtractor`. `FromContext` / `MustFromContext` helpers. `ErrNoTenant` for pass-through on non-tenant routes.
  - `idempotency/` — Idempotency-Key middleware (draft-ietf-httpapi-idempotency-key-header). Captures first response, replays on subsequent requests. 5xx not cached. Pluggable `Store`; `MemoryStore` for tests. `Options{Required, TTL, Header}`.
  - `flags/` — Typed feature-flag client. OpenFeature-compatible `Provider` interface (`Evaluate(ctx, key, default, evalCtx) (any, error)`). `Client.Bool/String/Int/Float` with safe defaults. Ships `MemoryProvider` (for tests) + `NoopProvider` (safe null object). `EvalContext map[string]any` for targeting.
  - All packages: `AGENTS.md` + full test suites. 0 lint issues.
- 2026-04-13: **Step 11 — webhooks/in + webhooks/out** landed:
  - `webhooks/in/` — Inbound signature verification middleware (no fx). `Stripe`, `GitHub`, `GitHubLegacy`, `Slack`, `HMAC` factories. Body buffered up to 1 MiB; replaced on `r.Body` for downstream reads. Timestamp replay guard on Stripe + Slack (5-min window).
  - `webhooks/out/` — Outbound delivery. `Dispatcher.Dispatch` signs with HMAC-SHA256 (`sha256=<hex>` in configurable header, matching `in.HMAC`), delivers to all active matching endpoints with exponential-backoff retry. Dead-letter after `MaxAttempts`. `Replay` re-runs a dead-lettered delivery. Pluggable `Store` interface for endpoint + delivery persistence. Injects `clock.Clock` for testable time. Uses `clockwork.FakeClock` in tests (no sleeping).
  - All packages: `AGENTS.md` + full test suites. 0 lint issues.
- 2026-04-14: **Step 10 — Notify + Realtime** landed (partial — Resend/Postmark/Slack senders + inbound/tracking/bounce deferred):
  - `notify/` — unified `Notifier` (first-success + multi fan-out). `Sender` interface. `SMTPSender` via wneessen/go-mail (TLS/STARTTLS/NoTLS). `WithSender` option.
  - `notify/unsub/` — RFC 8058 one-click unsubscribe. HMAC-signed URLs, POST+GET handler, pluggable `Store` (Add/IsSuppressed/Remove). `IsSuppressed` check for pre-send gating.
  - `realtime/sse/` — SSE hub. `Hub.Handler()` upgrades HTTP connection; `Hub.Publish` broadcasts to all clients. Per-client buffered channel; slow clients drop events non-blocking.
  - `realtime/pubsub/` — In-process `LocalBus` implementing `Bus` interface (Publish/Subscribe). Unsubscribe via cancel func. Cross-replica backends implement same interface.
  - All packages have `AGENTS.md` + tests.
- 2026-04-14: **Step 9 — Auth + Authz** landed (partial — passkeys/magiclink/lockout/oauth2server deferred):
  - `auth/jwt/` — HMAC signer (HS256/384/512) wrapping golang-jwt/v5. `NewHMACSigner`, `Sign`, `Parse`, `ErrExpired`, `ErrInvalid`. Pure utility, no fx.
  - `auth/apikey/` — HMAC-SHA256 API key issuance + verification. `Service.Issue/Verify/Revoke/ListByOwner`. Pluggable `Store` interface. Keys stored as hash; raw shown once.
  - `auth/oidc/` — OIDC + OAuth2 PKCE client via go-oidc/v3. `Provider.AuthURL/Exchange/UserInfo`. fx `Module` provides `*oidc.Provider`. Config: `auth.oidc.*`.
  - `auth/session/` — Server-side sessions. `Manager.Load/Save/Destroy`. Pluggable `Store`. Ships `MemoryStore` for tests. Cookie flags: HttpOnly, Secure, SameSite.
  - `authz/` — Casbin v2 RBAC/ABAC enforcer. `Module` provides `*Enforcer`. Ships `ModelRBAC` + `ModelRBACWithDeny` DSL constants. `NewFileAdapter` + `NewEnforcerForTest` helpers.
  - `golusoris.AuthOIDC` + `golusoris.Authz` umbrella vars added.
  - All packages have `AGENTS.md` + tests.
- 2026-04-14: **Step 8 — Cache** landed:
  - `cache/memory/` — otter v2 in-process L1 cache. `memory.Module` provides `*memory.Cache`. `memory.Typed[K,V](c, prefix)` gives a type-safe namespaced view. `NewForTest` for test use. Config: `cache.memory.{max_size,ttl}`.
  - `cache/redis/` — rueidis fx module. Auto-detects standalone vs cluster from `InitAddress`. Config: `cache.redis.{addr,user,pass,db,tls}`.
  - `cache/singleflight/` — typed wrapper over `golang.org/x/sync/singleflight`. No fx wiring needed — construct directly with `singleflight.New[K,V]()`.
  - `testutil/redis/` — testcontainers-go Redis harness. `redistest.Start(t)` returns a `rueidis.Client` + tears down via `t.Cleanup`.
  - `golusoris.CacheMemory` + `golusoris.CacheRedis` umbrella vars added to `golusoris.go`.
  - All packages have `AGENTS.md`.
- 2026-04-14: **Docs + community scaffolding** landed:
  - `docs/adr/` — Nygard template (`0000-template.md`), index README with backfill policy (linked to [joelparkerhenderson/architecture-decision-record](https://github.com/joelparkerhenderson/architecture-decision-record)), 7 backfilled ADRs (ADR-0001 through ADR-0007) covering fx, koanf, slog, ogen, river, pluggable-leader, RFC 9457.
  - `docs/architecture/` — C4-PlantUML README, L1 context diagram (`context.puml`), L2 container diagram (`container.puml`). L4 intentionally omitted.
  - `.github/ISSUE_TEMPLATE/` — `bug_report.yml`, `feature_request.yml`, `config.yml` (disables blank issues, links to Discussions + private security advisory).
  - `.github/PULL_REQUEST_TEMPLATE.md` — type-of-change checklist tied to PLAN + AGENTS.md sync rules.
  - `.github/CODEOWNERS` — `@lusoris` default + specific owners for security-sensitive paths.
  - `CODE_OF_CONDUCT.md` — Contributor Covenant v2.1.
  - `.markdownlintignore` — excludes CODEOWNERS (not markdown).
  - PLAN.md §2.4 expanded with ADR backfill policy + C4 tooling notes.
- 2026-04-13: **Step 6c — k8s/client** landed (closes Step 6): `*rest.Config` + `kubernetes.Interface` resolved with cascade in-cluster → KUBECONFIG → ~/.kube/config. Reports `Source` ("in-cluster" / "kubeconfig") for diagnostics. Workload identity (GKE / EKS IRSA / Azure AD WI) uses the standard SA-token mount — cloud SDKs read AWS_WEB_IDENTITY_TOKEN_FILE / AZURE_FEDERATED_TOKEN_FILE / metadata server themselves. golusoris.K8s umbrella wires podinfo + client; health/metrics/leader register via fx.Invoke (they take Registry/Callbacks args).
- 2026-04-13: **Step 5 — OTel + observability** landing in 2 commits:
  - 5a (`feat(otel)`): full SDK — tracer (OTLP batch + parent/TraceIDRatio sampler), meter (OTLP 15s periodic), logger (OTLP batch), W3C TraceContext+Baggage propagator, resource attrs from service.{name,version,namespace} + process + k8s downward-API pod metadata. `otel.ModuleWithSlogBridge` fans slog to the OTel logger provider alongside the local handler. `otel.enabled=false` = no-op.
  - 5b (`feat(observability)`): sentry (slog bridge: Error→event, Warn→breadcrumb; fx-Stop flush), profiling (Pyroscope in-process, off by default; eBPF mode deferred to deploy/ manifests per §4.7), pprof (auth-gated /debug/pprof with constant-time basic-auth), statuspage (HTML + JSON /status page backed by a shared check Registry used by k8s/health later). `observability/AGENTS.md` parent guide.

### Decisions made during Step 5

| Topic | Choice | Why |
|---|---|---|
| Commit shape | 2 commits (5a otel / 5b extras) | otel is foundational; extras build on it. |
| otel scope | Full SDK (tracer + meter + logs + OTLP + slog bridge add-on) | Matches PLAN §4.7; podinfo resource attrs from env vars until k8s/podinfo lands in Step 6. |
| Sentry bridge severity | Error→event, Warn→breadcrumb | Sentry is for errors, not info/debug noise. |
| Pyroscope mode | In-process in Go package, eBPF as deploy manifests | eBPF is a daemonset concern, not Go code. |
| statuspage | Shared Registry drives /status + /livez + /readyz (Step 6) | Single source of truth for "is this app healthy". |

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
