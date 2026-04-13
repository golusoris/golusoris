# golusoris — Framework Plan (consolidated)

> **Source of truth for the framework design.** Any future session (human or AI) can resume from this file alone.
> Last update: 2026-04-13. Decisions across all design rounds locked in.

---

## 1. Mission

Build `github.com/golusoris/golusoris` — a single Go module providing opt-in `fx` modules so that all `lusoris/*` apps (revenge, lurkarr, subdo, arca, future) share one source of truth for cross-cutting concerns. Bumping a dependency happens once in the framework, not 4–5× per app.

Secondary goal: bake AI-assisted-development conventions (AGENTS.md / CLAUDE.md / Skills / Hooks / cached docs / MCP server) into the repo so working on golusoris-based apps with Claude/Cursor/etc. is fast and cheap.

Tertiary goal: bake a **dependency-update + meaningful-changelog system** so we stop fetching garbage into the projects and every change is documented with breaking-change notes + codemods.

---

## 2. Architecture

**Single Go module, opt-in fx subpackages.** Apps import `github.com/golusoris/golusoris` and compose only the modules they need.

```go
fx.New(
  golusoris.Core,            // config + log + lifecycle + podinfo + errors
  golusoris.DB,              // pgx pool + migrations + sqlc
  golusoris.OTel,            // tracer + meter + logs + OTLP
  golusoris.HTTP,            // server + standard middleware + Scalar docs
  golusoris.Auth.OIDC, golusoris.Auth.Passkeys, golusoris.Auth.MagicLink,
  golusoris.Authz,
  golusoris.Jobs, golusoris.Outbox,
  golusoris.Cache.Memory, golusoris.Cache.Redis,
  golusoris.K8s.Health, golusoris.K8s.Leader, golusoris.K8s.PromMetrics,
  golusoris.Notify, golusoris.Realtime,
  // ... 80+ optional modules available
)
```

Lurkarr migration: out-of-scope. Framework converges to subdo/revenge/arca conventions.

---

## 3. Module catalog (organized)

### 3.1 Core

| Path | Purpose | Key dep |
|---|---|---|
| `config/` | koanf v2 wrapper, env+file+yaml, file-watch enabled (k8s ConfigMap hot reload), SIGHUP hook | knadh/koanf/v2 |
| `log/` | slog factory: tint(dev) / json(prod), podinfo attrs, otelslog bridge | log/slog + lmittmann/tint + go.opentelemetry.io/contrib/bridges/otelslog |
| `errors/` | typed errors, stack traces, ogen-status mapping | go-faster/errors |
| `crypto/` | argon2id, AES-GCM, sealed-secret helpers, column encryption | alexedwards/argon2id + stdlib |
| `clock/` | Clock interface (real + fake) for mockable time | jonboulle/clockwork |
| `id/` | UUIDv7, KSUID, snowflake generators | google/uuid + segmentio/ksuid |
| `validate/` | go-playground/validator wrapper | go-playground/validator/v10 |
| `i18n/` | locale negotiation middleware, message catalog | nicksnyder/go-i18n + x/text |

### 3.2 Database & data

| Path | Purpose | Key dep |
|---|---|---|
| `db/pgx/` | pool fx module + lifecycle + retry-on-startup + slow query logger | jackc/pgx/v5 |
| `db/migrate/` | golang-migrate v4 runner + fx hook + CLI helper | golang-migrate/migrate/v4 |
| `db/sqlc/` | shared sqlc.yaml fragment + helpers | sqlc-dev/sqlc (tool) |
| `db/bun/` | optional ORM module (alternative to sqlc) | uptrace/bun |
| `outbox/` | transactional outbox: write events in same tx, drained to jobs | custom on pgx |

### 3.3 HTTP / API

| Path | Purpose | Key dep |
|---|---|---|
| `httpx/server/` | http.Server + graceful shutdown + body limits + slow-loris guards | stdlib |
| `httpx/router/` | chi router for non-ogen routes (admin, webhooks, static) | go-chi/chi |
| `httpx/middleware/` | logger, recover, requestid, otel, secure-headers, compress, etag, trust-proxy | composite |
| `httpx/client/` | retry + circuit-breaker + OTel-instrumented HTTP client | sony/gobreaker + hashicorp/go-retryablehttp |
| `httpx/extclient/` | external API client factory: generate typed clients from third-party OpenAPI specs (via ogen) with rate-limit-respect + response cache (TTL+ETag) + OTel spans | ogen-go/ogen (tool) |
| `httpx/cors/` | CORS middleware | rs/cors |
| `httpx/csrf/` | CSRF middleware | gorilla/csrf |
| `httpx/ratelimit/` | per-IP/per-user rate limiting | ulule/limiter/v3 |
| `httpx/ws/` | websocket fan-out helpers | coder/websocket |
| `httpx/form/` | HTML form → struct decoder | go-playground/form |
| `httpx/static/` | static file serving + ETag + cache headers | stdlib |
| `httpx/static/hashfs/` | hashed-asset embed FS + transparent gzip/brotli | benbjohnson/hashfs + CAFxX/httpcompression |
| `httpx/vite/` | Vite manifest reader for hashed asset URLs | stdlib json |
| `httpx/htmx/` | HX-* response header helpers | custom |
| `httpx/inertia/` | Inertia.js adapter | romsar/gonertia |
| `httpx/autotls/` | autocert/Let's Encrypt OR certmagic (pluggable) | x/crypto/acme + caddyserver/certmagic |
| `httpx/geofence/` | IP/country allow-deny middleware | oschwald/maxminddb-golang |
| `ogenkit/` | ogen server adapter, error mapper, middleware glue | ogen-go/ogen |
| `apidocs/` | Scalar UI handler (`/docs`) + MCP-from-OpenAPI exposer (`/mcp`) | Scalar (JS, embedded) |

### 3.4 Auth & identity

| Path | Purpose | Key dep |
|---|---|---|
| `auth/oidc/` | OIDC + session storage | coreos/go-oidc/v3 |
| `auth/passkeys/` | webauthn + TOTP (optional MFA) | go-webauthn/webauthn + pquerna/otp |
| `auth/jwt/` | JWT helpers | golang-jwt/jwt/v5 |
| `auth/apikey/` | API key issuance, rotation, scopes, HMAC | custom |
| `auth/magiclink/` | passwordless email link sign-in | custom + notify |
| `auth/linking/` | account linking (multiple OIDC providers per user) | custom |
| `auth/impersonate/` | audited admin impersonation w/ banner + auto-revert | custom |
| `auth/session/` | session storage + manage-UI helpers (list + revoke) | custom on pgx/redis |
| `auth/recovery/` | recovery codes + forgot-password flow | custom + notify |
| `auth/policy/` | password strength (zxcvbn) + breach-list (HIBP k-anon) | nbutton23/zxcvbn-go |
| `auth/lockout/` | login rate-limit + lockout w/ cooldown | custom on cache |
| `auth/oauth2server/` | be an IdP, issue tokens to other apps | ory/fosite |
| `auth/scim/` | SCIM v2 user/group provisioning | custom |
| `auth/captcha/` | Cloudflare Turnstile + hCaptcha verify middleware | custom |
| `authz/` | RBAC/ABAC policy enforcement | casbin/casbin/v2 |

### 3.5 Background work

| Path | Purpose | Key dep |
|---|---|---|
| `jobs/` | river client + worker registry, periodic helpers, river-ui mount | riverqueue/river |
| `jobs/cron/` | cron expression parser/validator | robfig/cron/v3 |

### 3.6 Caching

| Path | Purpose | Key dep |
|---|---|---|
| `cache/memory/` | typed in-memory L1 cache | maypok86/otter/v2 |
| `cache/redis/` | rueidis fx module, distributed locks | redis/rueidis |
| `cache/singleflight/` | de-dupe concurrent identical reads | golang.org/x/sync/singleflight |

### 3.7 Observability

| Path | Purpose | Key dep |
|---|---|---|
| `otel/` | full OTel SDK (tracer + meter + logs) + OTLP exporter | go.opentelemetry.io/otel |
| `observability/sentry/` | Sentry fx module, slog/OTel bridged | getsentry/sentry-go |
| `observability/profiling/` | Pyroscope (in-process AND eBPF mode) | grafana/pyroscope-go |
| `observability/pprof/` | auth-gated /debug/pprof endpoint | stdlib |
| `observability/statuspage/` | public /status page (uptime + dep health) | custom |
| `k8s/health/` | /livez /readyz /startupz + check registry | alexliesenfeld/health (base) |
| `k8s/metrics/prom/` | Prometheus /metrics endpoint | prometheus/client_golang |

### 3.8 Kubernetes runtime

| Path | Purpose | Key dep |
|---|---|---|
| `k8s/podinfo/` | downward-API env → fx-provided PodInfo | stdlib |
| `k8s/leader/` | k8s Lease leader election | k8s.io/client-go |
| `k8s/client/` | client-go wrapper, in-cluster + kubeconfig + workload identity | k8s.io/client-go |

### 3.9 Notifications & realtime

| Path | Purpose | Key dep |
|---|---|---|
| `notify/` | unified Notifier: SMTP, Resend, Postmark, SES, Discord, Slack, webhook, web-push | wneessen/go-mail + provider SDKs |
| `notify/inbound/` | inbound email parsing (SES inbound, Postmark, SMTP) | custom |
| `notify/tracking/` | open/click tracking pixels + redirect | custom |
| `notify/unsub/` | RFC 8058 one-click unsubscribe + suppression list | custom |
| `notify/bounce/` | SES/Postmark bounce/complaint webhook handlers | custom |
| `realtime/sse/` | Server-Sent Events handler | r3labs/sse |
| `realtime/pubsub/` | pub/sub abstraction: pg LISTEN/NOTIFY, redis pubsub, NATS | custom + nats-io/nats.go |
| `realtime/webrtc/` | optional: peer streaming for low-latency media | pion/webrtc |

### 3.10 Webhooks

| Path | Purpose | Key dep |
|---|---|---|
| `webhooks/out/` | outbound delivery: register + sign + retry + dead-letter + replay | custom |
| `webhooks/in/` | inbound verification middleware: Stripe, GitHub, Resend, Slack, etc. | stdlib hmac |

### 3.11 SaaS primitives

| Path | Purpose | Key dep |
|---|---|---|
| `tenancy/` | tenant context middleware, tenant-scoped DB helpers, membership/invite | custom |
| `idempotency/` | Idempotency-Key middleware (redis/pg store) | custom on cache |
| `flags/` | OpenFeature SDK + postgres-backed provider | open-feature/go-sdk |
| `audit/` | append-only audit events (actor/action/target/diff) | custom on pgx |
| `page/` | typed cursor + offset pagination for sqlc/ogen | custom |

### 3.12 Files / storage / media

| Path | Purpose | Key dep |
|---|---|---|
| `storage/` | Bucket interface + backends (local, s3, minio, gocloud-portable) | aws/aws-sdk-go-v2 + gocloud.dev/blob |
| `storage/presign/` | S3 direct browser upload helpers | aws/aws-sdk-go-v2 |
| `storage/tus/` | resumable uploads (tus protocol) | tus/tusd |
| `storage/safety/` | EXIF strip + SSRF guards + path-traversal protection | custom |
| `storage/scan/` | ClamAV malware scan for uploads | custom + clamd client |
| `archive/` | extract/create zip/tar/rar/7z | mholt/archives |
| `media/av/` | FFmpeg wrapper (transcode, probe) | asticode/go-astiav |
| `media/img/` | image processing (resize/format) | davidbyttow/govips/v2 |
| `media/img/pipeline/` | on-demand resize + signed-URL serving | davidbyttow/govips/v2 |
| `media/audio/` | audio decode/encode/analyze | faiface/beep |
| `media/cv/` | computer vision (face/scene detect, video thumbnails) | hybridgroup/gocv |
| `ocr/` | text extraction from images/PDFs | otiai10/gosseract (tesseract) |
| `pdf/` | PDF generation (HTML→PDF) | chromedp/chromedp |
| `pdf/parse/` | PDF parsing (text/metadata/pages) | pdfcpu/pdfcpu |
| `markdown/` | Markdown rendering (GFM) | yuin/goldmark |
| `htmltmpl/` | type-safe HTML templates | a-h/templ |
| `jsonschema/` | JSON Schema validation (external schemas) | santhosh-tekuri/jsonschema |
| `hash/` | content hashing (xxhash, blake3, sha256) helpers | cespare/xxhash + zeebo/blake3 |
| `fs/watch/` | recursive directory watch w/ debounce | fsnotify/fsnotify |
| `httpx/rangeserve/` | HTTP range/streaming server (video playback) | stdlib |
| `torrent/` | torrent client abstraction: interface with backends for rtorrent / qbittorrent / transmission | autobrr/go-rtorrent + custom backends |

### 3.13 Search & AI

| Path | Purpose | Key dep |
|---|---|---|
| `search/` | Indexer/Searcher iface + backends: typesense, meilisearch, opensearch, pg FTS | typesense/typesense-go/v2 + others |
| `ai/llm/` | unified Chat/Stream/Embed interface (Anthropic + OpenAI + Ollama) | anthropics/anthropic-sdk-go + others |
| `ai/vector/` | pgvector schema + embeddings + similarity + hybrid search | pgvector/pgvector-go |

### 3.14 Commerce

| Path | Purpose | Key dep |
|---|---|---|
| `payments/stripe/` | Stripe wrapper + webhook verify + checkout + portal | stripe/stripe-go |
| `payments/subs/` | plans/seats/trial/proration/dunning state machine | custom |
| `payments/meter/` | usage metering w/ idempotency + exports | custom on outbox |
| `payments/invoice/` | PDF invoicing w/ sequential numbering | uses pdf/ + storage/ |
| `money/` | currency-aware money type | govalues/decimal + Rhymond/go-money |

### 3.15 Integrations

| Path | Purpose | Key dep |
|---|---|---|
| `integrations/goenvoy/` | thin fx adapter — wires shared HTTP client + retry/cache/OTel around the external `github.com/golusoris/goenvoy` clients (arr/*, tmdb, anilist, trakt). Goenvoy itself stays a separate repo. | github.com/golusoris/goenvoy |
| `geoip/` | maxmind GeoLite2 lookups | oschwald/maxminddb-golang |
| `secrets/` | Secret iface + backends: env, file, Vault, AWS SM, GCP SM, k8s ExternalSecrets | hashicorp/vault/api + cloud SDKs |

### 3.16 Big alternative stacks (opt-in, heavier surface)

| Path | Purpose | Key dep |
|---|---|---|
| `internal/grpc/` | grpc-go + buf-based codegen (ConnectRPC variant available) | grpc/grpc-go + connectrpc/connect-go |
| `internal/graphql/` | gqlgen fx module (server) | 99designs/gqlgen |
| `graphql/client/` | typed GraphQL client (consumes external GQL) | Khan/genqlient |
| `jobs/workflow/` | Temporal workflow orchestration | temporalio/sdk-go |
| `db/geo/` | PostGIS pgx type handlers + geometry helpers | custom on pgx |
| `db/timescale/` | TimescaleDB hypertables + retention | custom on pgx |
| `db/clickhouse/` | ClickHouse OLAP client | ClickHouse/clickhouse-go/v2 |
| `pubsub/kafka/` | Kafka/JetStream streaming | twmb/franz-go |
| `pubsub/nats/` | NATS JetStream | nats-io/nats.go |
| `outbox/cdc/` | CDC drain of outbox to Kafka/NATS/webhooks | custom |
| `ebpf/` | cilium/ebpf wrapper for apps loading custom eBPF programs | cilium/ebpf |

### 3.17 Misc utilities

| Path | Purpose | Key dep |
|---|---|---|
| `clikit/` | cobra wrapper, fx-aware app commands | spf13/cobra |
| `clikit/tui/` | TUI helpers for interactive CLIs | charmbracelet/bubbletea |
| `selfupdate/` | binary self-update from GitHub releases | minio/selfupdate |
| `plugin/` | extension-point system for apps | custom (Go plugin / wasm / RPC) |

### 3.18 Testing

| Path | Purpose | Key dep |
|---|---|---|
| `testutil/pg/` | testcontainers postgres | testcontainers-go/postgres |
| `testutil/redis/` | testcontainers redis | testcontainers-go/redis |
| `testutil/river/` | in-memory river test queue | riverqueue/river |
| `testutil/fxtest/` | fx.New helpers for tests | go.uber.org/fx/fxtest |
| `testutil/snapshot/` | snapshot testing helper | gkampitakis/go-snaps |
| `testutil/factory/` | test factories + faker | brianvoe/gofakeit |
| `testutil/fuzz/` | fuzz test corpora helpers | stdlib |
| `testutil/load/` | vegeta-driven load test helpers | tsenart/vegeta |
| `testutil/mutation/` | mutation testing helper | avito-tech/go-mutesting |

### 3.19 CLI binaries

| Path | Purpose |
|---|---|
| `cmd/golusoris/` | scaffolder CLI: `init`, `add`, `bump` (codemods) |
| `cmd/golusoris-mcp/` | MCP server exposing framework as tools to MCP clients |

### 3.20 Deploy artifacts

| Path | Purpose |
|---|---|
| `deploy/helm/` | base Helm chart (deployment, svc, cm, secret, hpa, servicemonitor, pdb, networkpolicy, **ciliumnetworkpolicy**, downward-API env, backup CronJob) |
| `deploy/observability/` | Grafana dashboards (HTTP/DB/river/runtime) + PrometheusRule manifests |
| `deploy/observability/otel-autoinst/` | OpenTelemetry Go auto-instrumentation (eBPF-based, zero-code) sidecar/daemonset manifests — complements in-process OTel SDK |
| `deploy/logging/` | Loki/Promtail manifests |
| `deploy/terraform/` | Terraform modules (S3/GCS, postgres, redis, networking) |
| `deploy/pulumi/` | optional Pulumi Go IaC |
| `deploy/flux/` + `deploy/argocd/` | GitOps example manifests |
| `deploy/multiregion/` | docs + example manifests for active-passive multi-region deployments (GeoDNS, failover, DB follower routing) |

### 3.21 Tools (Makefile / linter / hot-reload / templates)

| Path | Purpose |
|---|---|
| `tools/Makefile.shared` | included from app Makefiles |
| `tools/golangci.yml` | full linter set (govet, staticcheck, errcheck, gosec, govulncheck, gocritic, revive, gocyclo, funlen, gocognit, bodyclose, rowserrcheck, sqlclosecheck, errorlint, wrapcheck, gci, gofumpt, misspell, godot, whitespace) |
| `tools/mockery.yaml` | mockery v3 config template |
| `tools/air.toml` | hot-reload config |
| `tools/Dockerfile.template` | multi-stage, Chainguard-static base, non-root, HEALTHCHECK |
| `tools/Dockerfile.media.template` | variant w/ tesseract + libav + libvips + opencv system deps (Chainguard-wolfi base) |
| `tools/docker-compose.dev.yml` | postgres + redis + app w/ air |
| `tools/docker-compose.prod.yml` | production-like local stack |
| `tools/.goreleaser.yml` | multi-arch buildx + GHCR push + syft SBOM + cosign keyless + SLSA L3 |
| `tools/spectral.yaml` | OpenAPI lint config (or vacuum) |
| `tools/.pre-commit-config.yaml` | gofumpt + golangci + gitleaks + commitlint hooks |

### 3.22 GitHub repo template (reusable workflows + scaffold)

| Path | Purpose |
|---|---|
| `.github/workflows/ci-go.yml` | **reusable** workflow (apps `uses:`): lint + vuln + gosec + test + cover + apidiff |
| `.github/workflows/release-go.yml` | **reusable** workflow: release-please + goreleaser + multi-arch + GHCR + SBOM + cosign + SLSA |
| `.github/workflows/codeql.yml` | reusable CodeQL Go scanning |
| `.github/workflows/scorecard.yml` | reusable OSSF Scorecard |
| `.github/workflows/rebuild-on-base.yml` | reusable: triggered when Renovate bumps base image digest → rebuild + push |
| `template/.github/` | per-app stub: thin wrappers calling the reusable workflows above + dependabot.yml + renovate.json + ISSUE_TEMPLATE/ + PULL_REQUEST_TEMPLATE.md + CODEOWNERS + FUNDING.yml (kofi) + SECURITY.md + CONTRIBUTING.md |
| `template/.devcontainer/` | postgres + redis + Go + air + golangci preinstalled |

### 3.23 AI / agent layer

| Path | Purpose |
|---|---|
| `AGENTS.md` (root + per-subpackage) | cross-tool agent guide (Claude, Cursor, Aider, Codex, Continue) |
| `CLAUDE.md` (root) | Claude-specific deeper guide |
| `.claude/skills/` | scaffold-ogen-handler, add-river-worker, wire-fx-module, bump-golusoris, add-migration |
| `.claude/hooks/` | file-pattern triggered context auto-loads (e.g. touching `internal/jobs/*.go` loads river docs) |
| `docs/upstream/` | cached/snapshotted upstream docs for offline AI reasoning (fx, ogen, pgx, river, otter, rueidis, koanf, casbin, webauthn, OTel, golang-migrate, sqlc, scalar, k8s, etc.) |
| `docs/migrations/vX.Y.Z.md` | per-version migration guides w/ before/after snippets and codemod references |

---

## 4. Pinned versions (initial v0.1.0)

(see §3 for the per-module dep). Toolchain: **Go 1.26.2**. Versions tracked + bumped via Renovate; framework's CHANGELOG includes "Dependencies bumped" section per release.

---

## 5. Linting / security baseline

`tools/golangci.yml` enables the full set listed in §3.21. Standard make targets (`lint`, `vuln`, `gosec`, `sec`, `test`, `ci`, `gen`, `migrate`, `dev`, `mocks`).

Pre-commit hooks: gofumpt + golangci-lint + gitleaks (secret scan) + conventional-commit check.

CI gates (in reusable `ci-go.yml`):
- golangci-lint
- govulncheck
- gosec
- go test -race -count=1 + coverage upload
- **apidiff vs previous tagged release** — fails if public symbol changes without `!` / `BREAKING CHANGE:` footer
- **OpenAPI spec lint** (spectral / vacuum) for ogen specs
- **PR title check** (conventional commits enforcement)

---

## 6. AI / tooling layer (the part that compounds across all apps)

See §3.23 for files. Key behaviors:

- **`golusoris bump <version>` codemods**: each breaking change in framework can ship a codemod (Go AST rewriter via `golang.org/x/tools/go/analysis`). The CLI reads target version's migration notes, applies codemods, runs tests, opens a PR with mechanical changes + a checklist of manual ones.
- **MCP server `cmd/golusoris-mcp/`**: exposes `lookup_package`, `scaffold(kind, args)`, `list_modules`, `list_migrations(app)` to Claude/Cursor.
- **Scalar API docs + MCP**: `apidocs/` mounts Scalar UI at `/docs` and serves MCP-from-OpenAPI at `/mcp` so AI agents can hit running endpoints as tools.

---

## 7. GitHub repo template — **reusable workflows + scaffolder**

Apps' `.github/workflows/ci.yml` is ~20 lines:

```yaml
jobs:
  ci:
    uses: golusoris/golusoris/.github/workflows/ci-go.yml@v1
    with:
      has_frontend: true
      has_migrations: true
      go_version: '1.26.2'
```

**`golusoris init my-app`** generates the per-app stub. Changing CI logic = update one reusable workflow in the framework, all 5 apps inherit on next workflow run.

**Per-app files**:
- thin workflow wrappers calling reusable workflows
- `renovate.json` + `dependabot.yml` (Dependabot for security alerts, Renovate for routine bumps)
- ISSUE_TEMPLATE/, PULL_REQUEST_TEMPLATE.md
- CODEOWNERS
- FUNDING.yml with **kofi link baked in**
- SECURITY.md, CONTRIBUTING.md
- .devcontainer/ for VS Code / Codespaces

---

## 8. Container layer

- **Base image**: **Chainguard** (chainguard/static for binary apps, chainguard/wolfi for media-heavy variants needing tesseract/libav/libvips/opencv). Daily upstream rebuilds.
- **Multi-stage Dockerfile.template** + media variant.
- **Multi-arch buildx**: amd64 + arm64.
- **Image tags** (every release):
  - `v1.2.3`, `1.2`, `1`, `latest` (stable)
  - `sha-abc1234` (immutable per commit)
  - `pr-123`, `main-abc1234` (preview, GC'd after 30d)
  - Pre-release: `v1.2.3-rc.1`, `-beta.1`, `-alpha.1`
- **Required on every release**: cosign keyless signature + syft SBOM + SLSA L3 provenance.
- **Auto-rebuild on base update**: Renovate bumps Chainguard digest → triggers `rebuild-on-base.yml` → rebuild + re-sign + push (without a code change).
- **Hot config reload**: koanf file-watch on mounted ConfigMap (`config/`). SIGHUP also re-reads.
- **Zero-downtime rollout**: k8s rolling update + readiness probes + PodDisruptionBudget (in `deploy/helm/`).

(Note: kernel live-patching is OS-level — handled by node OS, not framework.)

---

## 9. Versioning + dependency-update + changelog system

- **Branching**: trunk-based, single `main`.
- **Releases**: release-please bot generates release PRs from conventional commits; merging tags `vX.Y.Z`.
- **Conventional commit gate** on PR titles enforced by CI.
- **Custom `Migration:` footer** required for any commit with `!` / `BREAKING CHANGE:` — contains before/after code. Auto-stitched into `docs/migrations/vX.Y.Z.md`.
- **apidiff CI gate**: forbids undeclared API breakage.
- **Deprecations**: stdlib `// Deprecated:` doc convention; staticcheck SA1019 surfaces uses.
- **Auto-generated CHANGELOG sections per release**:
  - Features / Fixes / Breaking changes (release-please)
  - Migration recipes (collected from `Migration:` footers)
  - **Dependencies bumped** (script diffs `go.mod` between tags; lists transitive changes)
- **Renovate** for routine dep bumps in framework + apps; **Dependabot** for security alerts. Auto-merge minor/patch on green CI; majors require human review.
- **`golusoris bump` CLI**: applies migration codemods automatically when bumping framework in an app.

---

## 10. Build order (suggested)

Each step a tagged `v0.x.0`. Framework usable from step 3.

1. **Skeleton + Core** — go.mod, golusoris.go, config/, log/, errors/, clock/, id/, validate/, i18n/, crypto/, tools/, root AGENTS.md + CLAUDE.md.
2. **DB** — db/pgx, db/migrate, db/sqlc, testutil/pg.
3. **HTTPX (base)** — httpx/server, middleware, router (chi), client; ogenkit + apidocs (Scalar).
4. **HTTPX (extras)** — cors, csrf, ratelimit, ws, form, static, hashfs, vite, htmx, geofence, autotls.
5. **OTel + observability** — otel/, sentry/, profiling/, pprof/, statuspage/.
6. **K8s runtime** — k8s/health (alexliesenfeld base), podinfo, metrics/prom, leader, client (with workload identity).
7. **Jobs + outbox** — jobs/, jobs/cron, outbox/, testutil/river.
8. **Cache** — cache/memory, cache/redis, cache/singleflight, testutil/redis.
9. **Auth + authz** — oidc, passkeys, jwt, apikey, magiclink, linking, impersonate, session, recovery, policy, lockout, captcha, authz.
10. **Notify + realtime** — notify/ + inbound + tracking + unsub + bounce; realtime/sse + pubsub.
11. **Webhooks** — webhooks/in, webhooks/out.
12. **SaaS primitives** — tenancy, idempotency, flags, audit, page.
13. **Files / storage / media** — storage/ + presign + tus + safety + scan; archive/; media/av + img + audio + cv; ocr/; pdf/ + parse; markdown/; htmltmpl/; jsonschema/; hash/; fs/watch; httpx/rangeserve.
14. **Search + AI** — search/, ai/llm, ai/vector.
15. **Commerce** — payments/stripe + subs + meter + invoice; money/.
16. **Integrations** — integrations/goenvoy (thin adapter around external `github.com/golusoris/goenvoy`); geoip/; secrets/.
17. **Big stacks (opt-in)** — grpc, graphql (server + client), workflow (Temporal), geo, timescale, clickhouse, kafka, nats, outbox/cdc, ebpf.
18. **Misc** — clikit + tui, selfupdate, plugin.
19. **Testing extras** — testutil/snapshot + factory + fuzz + load + mutation.
20. **CLI + MCP** — cmd/golusoris (init/add/bump), cmd/golusoris-mcp.
21. **Deploy** — deploy/helm (incl. ciliumnetworkpolicy, backup CronJob), deploy/observability (Grafana JSON + alert rules), deploy/logging (Loki/Promtail), deploy/terraform, deploy/pulumi, deploy/flux + argocd.
22. **GitHub template + reusable workflows** — `.github/workflows/*` (reusable) + `template/.github/` (per-app stubs).
23. **AI layer** — per-package AGENTS.md + CLAUDE.md, .claude/skills/ + hooks/, docs/upstream/ snapshots, docs/migrations/.
24. **Polish** — root README, examples/ folder showing minimal app composing 5 modules, framework's own CI green.

---

## 11. Decisions log (one-line each, comprehensive)

**Architecture**: single Go module + opt-in fx subpackages; lurkarr migration out of scope.

**Picks**: ogen v1.20.3 · golang-migrate v4.19.1 · sqlc v1.30.0 · koanf v2.3.4 (file-watch on) · river v0.34.0 · otter/v2 v2.3.0 · rueidis v1.0.74 · OTel SDK v1.43.0 · go-oidc v3.18.0 · webauthn v0.16.4 · TOTP v1.5.0 · casbin v2.135.0 · slog + tint v1.1.3 · rs/cors v1.11.1 · ulule/limiter v3.11.2 · coder/websocket v1.8.14 · gorilla/csrf v1.7.3 · sony/gobreaker v1.0.0 · go-faster/errors v0.7.1 · alexedwards/argon2id v1.0.0 · sentry-go v0.45.0 · go-astiav v0.40.0 · govips/v2 v2.18.0 · aws-sdk-go-v2 v1.41.5 · mholt/archives v0.1.5 · testcontainers v0.42.0 · testify v1.11.1 · **mockery v3** · k8s.io/client-go v0.32.x · prometheus/client_golang v1.23.2.

**Modules added across rounds** (all): notify (multi-channel), validate, realtime (sse + pubsub), secrets (Vault/cloud SM/k8s ESO), pagination, audit, idempotency, OpenFeature flags, clikit (cobra), clock, id (uuidv7+ksuid), i18n, htmltmpl (templ), search abstraction, money, pdf, fs/watch, httpx/rangeserve, hash, **torrent abstraction**, **httpx/extclient** (typed clients from 3rd-party OpenAPI), otelslog, river-ui, db seeders.

**SaaS primitives**: API keys, magic links, multi-tenancy, transactional outbox, account linking, impersonation, session mgmt + revoke, account recovery, password policy (zxcvbn), login lockout, OAuth2-server (fosite), SCIM, captcha (Turnstile/hCaptcha).

**Comms extras**: inbound email, tracking pixels, unsubscribe (RFC 8058), bounce/complaint handlers.

**Security extras**: column encryption, geofence (IP/country), upload safety (EXIF/SSRF/path-traversal), malware scan (clamav).

**Webhook + TLS**: outbound webhooks, inbound verification, AutoTLS (autocert + certmagic), external API client w/ retry+cache.

**Files/AI**: presigned uploads + image pipeline, tus resumable, pgvector + embeddings, LLM client (Anthropic+OpenAI+Ollama).

**Observability extras**: Pyroscope (in-process + eBPF), slow query logger, status page, synthetic monitoring.

**Frontend/geo**: Vite manifest, HTMX helpers, **gonertia (switched from petaki/inertia-go)**, GeoIP (maxmind).

**Testing extras**: snapshot, factories (gofakeit), fuzz, vegeta load, mutation testing.

**Commerce**: Stripe wrapper, subs state machine, usage metering, PDF invoicing, money/currency type (Rhymond/go-money).

**Big stacks (opt-in)**: gRPC + buf, GraphQL (gqlgen), Temporal workflows, PostGIS, TimescaleDB, ClickHouse, Kafka (franz-go), NATS, CDC outbox drain.

**Deploy/ops**: Loki/Promtail, DB backup CronJob, Terraform, Pulumi, Flux/ArgoCD, pre-commit hooks.

**Misc**: cron expression parser, plugin system, self-update binaries, multi-region/GeoDNS guidance (shipped as `deploy/multiregion/`).

**Awesome-go gaps filled**: markdown (goldmark), JSON Schema (jsonschema), form parsing (go-playground/form), hashfs static FS, optional ORM (uptrace/bun), gocloud.dev/blob portability, chi router for non-ogen routes, money type, GraphQL client (genqlient), certmagic alt for autocert, r3labs/sse, alexliesenfeld/health as base.

**eBPF/Cilium**: OTel Go auto-instrumentation (eBPF, zero-code, shipped as deploy/observability/otel-autoinst manifests), Pyroscope eBPF mode, CiliumNetworkPolicy in Helm chart, golusoris/ebpf wrapper module.

**Final extras**: CLI TUI (bubbletea), workload identity (GKE/EKS/Azure), GitOps (Flux/ArgoCD), OCR (gosseract), PDF parsing (pdfcpu), audio (beep), computer vision (gocv), OpenAPI lint (spectral), WebRTC (pion), Pulumi.

**Tooling/CI**: shared Makefile + air + full golangci config (gosec/govuln/gocritic/...) + mockery v3 + spectral lint + pre-commit hooks.

**GitHub template**: reusable workflows (`uses:` from apps) + scaffolder. Apps' `.github/` is thin stubs.

**Versioning**: trunk-based + release-please + conventional commits + apidiff CI gate + custom `Migration:` footer + auto-generated "Dependencies bumped" CHANGELOG section + `golusoris bump` CLI codemods.

**Container**: Chainguard base (static + wolfi for media), multi-arch (amd64+arm64), tags (`vX.Y.Z` + `1.2` + `1` + `latest` + `sha-*` + `pr-*` + `main-*` + `edge` + pre-release channels), cosign-signed + SBOM + SLSA L3, auto-rebuild on Chainguard digest update via Renovate.

**Dep updates**: Renovate (routine, grouped, auto-merge minor/patch) + Dependabot (security alerts only).

**API stability**: apidiff CI + Migration footer + Dependencies bumped section + `// Deprecated:` doc + staticcheck SA1019.

**AI layer**: AGENTS.md + CLAUDE.md (root + per-pkg) + .claude/skills/ + .claude/hooks/ + docs/upstream/ cache + docs/migrations/ + `golusoris-mcp` MCP server + Scalar-from-OpenAPI MCP per app.

**Plan persistence**: `.workingdir/PLAN.md` (this file).

---

## 12. Out of scope (explicit non-goals)

- Frontend (separate framework later)
- Bioinformatics, robotics, hardware GPIO/IoT, blockchain/web3
- Email server (receiving SMTP server itself; we *parse* inbound but don't run the server)
- Service mesh data plane (Cilium/Istio control)
- Property-based testing (gopter), Pact contract testing
- DOCX/XLSX writing, ePub generation
- Game engines / 3D
- Scientific computing (gonum) — apps add directly if they need it
- Plotting/charts
- ZFS/btrfs snapshots, wake-on-LAN, udev
- DNS server (miekg/dns) — apps add directly if needed
- Crossplane

---

## 13. Resolved settings

- **Module path**: `github.com/golusoris/golusoris` (new GitHub org `golusoris` — confirmed available 2026-04-13). Import package name is `golusoris` (no rename needed). User to register the org before `go mod init`.
- **Org structure (revised 2026-04-13 — Option B)**: All golusoris-family code lives under the `golusoris/` org. Naming convention:
  - Framework (namesake): `golusoris/golusoris`
  - Libraries: `golusoris/<name>` — e.g. `golusoris/goenvoy`
  - Apps: `golusoris/app-<name>` — e.g. `golusoris/app-lurkarr`, `golusoris/app-subdo`, `golusoris/app-revenge`, `golusoris/app-arca`
  - Tools/CLIs (future): `golusoris/cmd-<name>` (proposed)

  GitHub doesn't support nested orgs, so the `app-` prefix is the chosen visual grouping. The 4 existing apps will be transferred from `lusoris/` to `golusoris/` and renamed accordingly. `lusoris/.github` stays in place for any unrelated personal repos.
- **Goenvoy treatment**: separate repo at `github.com/golusoris/goenvoy` (org move). Framework provides a thin `integrations/goenvoy/` adapter (fx wiring + shared HTTP client) — NOT inlined. No source duplication.
- **License**: **MIT**.
- **Ko-fi handle**: `lusoris` (baked into `FUNDING.yml` template as `ko_fi: lusoris`).
- **Examples**: `examples/` folder in same repo.
- **MCP server transport**: stdio + HTTP (both supported).
- **Renovate grouping rules** (default; tweakable later): groups for `otel-*`, `k8s-*`, `aws-*`, `observability-*` (sentry/pyroscope/prom), `postgres-*` (pgx/migrate/sqlc), `riverqueue-*`, `redis-*` (rueidis/otter), `auth-*` (oidc/webauthn/casbin/jwt), `ogen-*`, `testcontainers-*`. Auto-merge minor/patch on green CI; majors require human review.
- **apidiff baseline**: every top-level subpackage is public-API tracked. `internal/` (any) is excluded (Go enforces). Per-package `AGENTS.md` notes which symbols are stable vs experimental (`Experimental:` doc comment marker).
