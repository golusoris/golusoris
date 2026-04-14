# golusoris

[![Go Reference](https://pkg.go.dev/badge/github.com/golusoris/golusoris.svg)](https://pkg.go.dev/github.com/golusoris/golusoris)
[![Go Report Card](https://goreportcard.com/badge/github.com/golusoris/golusoris)](https://goreportcard.com/report/github.com/golusoris/golusoris)
[![Go Version](https://img.shields.io/github/go-mod/go-version/golusoris/golusoris)](go.mod)
[![CI](https://github.com/golusoris/golusoris/actions/workflows/ci.yml/badge.svg)](https://github.com/golusoris/golusoris/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/golusoris/golusoris/badge)](https://scorecard.dev/viewer/?uri=github.com/golusoris/golusoris)
[![ko-fi](https://img.shields.io/badge/ko--fi-support-FF5E5B?logo=ko-fi&logoColor=white)](https://ko-fi.com/lusoris)

A composable Go framework built around [`go.uber.org/fx`](https://github.com/uber-go/fx). Pick the modules your app needs — nothing else ships. Every module follows the same [principles](docs/principles.md): Power-of-10 coding rules, SEI CERT secure-coding, Google Go Style, RFC 9457 error bodies, OTel SemConv v1.26, and SLSA L3 supply-chain standards.

---

## Quick start

```go
import "github.com/golusoris/golusoris"

fx.New(
    golusoris.Core,        // config · log · errors · clock · id · validate · crypto · i18n
    golusoris.DB,          // pgx pool · migrate · sqlc helpers
    golusoris.OTel,        // tracer · meter · logs · OTLP exporter
    golusoris.HTTP,        // server · chi router · middleware · Scalar API docs
    golusoris.K8s.Health,  // /livez  /readyz  /startupz
    golusoris.Jobs,        // river background jobs + cron
    golusoris.Cache.Redis, // rueidis distributed cache
    // ... add what you need
).Run()
```

All modules read their config from environment variables (prefix `APP_`) via koanf. No config files required.

---

## Principles

The full contract is in [docs/principles.md](docs/principles.md). Short form:

| # | Rule |
|---|---|
| **Coding** | NASA/JPL Power of 10 adapted for Go — no `goto`, bounded loops, ≤120-line functions, every error checked, 0 lint/gosec/govulncheck on merge |
| **Security** | SEI CERT for Go — safe crypto, input validation at every boundary, no `unsafe` outside reviewed hot-paths |
| **Style** | Google Go Style Guide (canonical) + Effective Go (secondary) |
| **Architecture** | C4 diagrams in `docs/architecture/`; Nygard ADRs in `docs/adr/` |
| **Supply chain** | SLSA Level 3 — SBOM, cosign signing, provenance attestation on every release |
| **Compliance** | OWASP ASVS L2 · NIST SSDF · EU CRA · NIS2 · BSI IT-Grundschutz · BSI C5 · UK NCSC · GDPR · EU AI Act scaffolding |
| **APIs** | RFC 9457 Problem Details · OpenAPI 3.1 · OTel SemConv v1.26 · JWT/OAuth 2.1/PKCE/WebAuthn |
| **Testing** | Table-driven tests · `-race` on every CI run · 70% coverage (85% security-critical) · real containers over mocks |
| **Deployment** | Twelve-Factor · CNCF cloud-native · OCI multi-arch · rootless + read-only FS |

Every merged commit: **0 lint · 0 gosec · 0 govulncheck · race-green.**

---

## Module catalog

### Core

| Module | Purpose | Key dep |
|---|---|---|
| `config/` | koanf v2 — env + file + YAML, file-watch (ConfigMap hot-reload), SIGHUP hook | knadh/koanf/v2 |
| `log/` | slog factory: tint (dev) / JSON (prod), podinfo attrs, OTel bridge | lmittmann/tint |
| `errors/` | typed errors, stack traces, ogen-status mapping | go-faster/errors |
| `crypto/` | argon2id, AES-GCM, sealed-secret helpers, column encryption | alexedwards/argon2id |
| `clock/` | mockable wall clock (real + fake) — `time.Now()` is banned outside this package | jonboulle/clockwork |
| `id/` | UUIDv7, KSUID, snowflake generators | google/uuid · segmentio/ksuid |
| `validate/` | go-playground/validator wrapper with i18n error messages | go-playground/validator/v10 |
| `i18n/` | locale negotiation middleware, message catalog | nicksnyder/go-i18n |

### Database & data

| Module | Purpose | Key dep |
|---|---|---|
| `db/pgx/` | pgx pool fx module + startup retry + slow-query logger | jackc/pgx/v5 |
| `db/migrate/` | golang-migrate v4 runner + fx lifecycle hook | golang-migrate/migrate/v4 |
| `db/sqlc/` | shared sqlc.yaml fragment + query helpers | sqlc-dev/sqlc |
| `db/geo/` | PostGIS pgx type handlers — Point, BBox, EWKB scanner, Haversine | custom on pgx |
| `db/timescale/` | TimescaleDB hypertable creation, retention, compression helpers | custom on pgx |
| `db/clickhouse/` | ClickHouse OLAP client fx module | ClickHouse/clickhouse-go/v2 |
| `db/cdc/` | PostgreSQL logical-replication (WAL) consumer — pgoutput decoder → `Event` | jackc/pglogrepl |
| `outbox/` | transactional outbox — write events in same tx, drain via river | custom on pgx |
| `outbox/cdc/` | CDC-based outbox drain → Kafka / NATS / Webhook sinks | uses db/cdc |

### HTTP / API

| Module | Purpose | Key dep |
|---|---|---|
| `httpx/server/` | `*http.Server` with slow-loris guards, body limits, graceful shutdown | stdlib |
| `httpx/router/` | chi router + http.Handler provided to fx graph | go-chi/chi |
| `httpx/middleware/` | logger, recovery, request-id, OTel, secure-headers, compress, ETag, trust-proxy | composite |
| `httpx/client/` | retry + circuit-breaker + OTel-instrumented HTTP client | sony/gobreaker |
| `httpx/cors/` | CORS middleware | rs/cors |
| `httpx/csrf/` | CSRF middleware | gorilla/csrf |
| `httpx/ratelimit/` | per-IP / per-user rate limiting | ulule/limiter/v3 |
| `httpx/ws/` | WebSocket fan-out helpers | coder/websocket |
| `httpx/form/` | HTML form → struct decoder | go-playground/form |
| `httpx/static/` | static file serving + ETag + cache headers | stdlib |
| `httpx/static/hashfs/` | hashed-asset embed FS + transparent gzip/brotli | benbjohnson/hashfs |
| `httpx/vite/` | Vite manifest reader for hashed asset URLs in templates | stdlib |
| `httpx/htmx/` | `HX-*` response header helpers | custom |
| `httpx/autotls/` | autocert / Let's Encrypt or certmagic (pluggable) | x/crypto/acme + certmagic |
| `httpx/geofence/` | IP / country allow-deny middleware | oschwald/maxminddb-golang |
| `httpx/rangeserve/` | HTTP range-request serving for video / large files | stdlib |
| `ogenkit/` | ogen server adapter, RFC 9457 error mapper, middleware glue | ogen-go/ogen |
| `apidocs/` | Scalar UI (`/docs`) + MCP-from-OpenAPI exposer (`/mcp`) | Scalar (JS, embedded) |

### Auth & identity

| Module | Purpose | Key dep |
|---|---|---|
| `auth/oidc/` | OIDC + PKCE + session storage | coreos/go-oidc/v3 |
| `auth/passkeys/` | WebAuthn + TOTP (MFA) | go-webauthn/webauthn + pquerna/otp |
| `auth/jwt/` | JWT issuance, validation, rotation | golang-jwt/jwt/v5 |
| `auth/apikey/` | API key issuance, rotation, scopes, HMAC | custom |
| `auth/magiclink/` | passwordless email-link sign-in | custom + notify |
| `auth/linking/` | multi-IdP identity linking per account | custom |
| `auth/impersonate/` | audited admin impersonation + banner + auto-revert | custom |
| `auth/session/` | server-side session storage, list + revoke UI | custom on pgx/redis |
| `auth/recovery/` | recovery codes + forgot-password flow | custom + notify |
| `auth/policy/` | password strength (zxcvbn) + breach check (HIBP k-anon) | nbutton23/zxcvbn-go |
| `auth/lockout/` | per-identity login rate-limit + cooldown | custom on cache |
| `auth/oauth2server/` | be an IdP — issue tokens to other apps (OAuth 2.1) | ory/fosite |
| `auth/scim/` | SCIM 2.0 user + group provisioning endpoint | custom |
| `auth/captcha/` | Cloudflare Turnstile + hCaptcha + reCAPTCHA verifier middleware | custom |
| `authz/` | RBAC / ABAC policy enforcement | casbin/casbin/v2 |

### Background work

| Module | Purpose | Key dep |
|---|---|---|
| `jobs/` | river client + worker registry + periodic helpers + river-ui mount | riverqueue/river |
| `jobs/cron/` | cron expression parser / validator | robfig/cron/v3 |
| `jobs/workflow/` | Temporal workflow orchestration | go.temporal.io/sdk |

### Caching

| Module | Purpose | Key dep |
|---|---|---|
| `cache/memory/` | typed in-memory L1 cache (TinyLFU eviction) | maypok86/otter/v2 |
| `cache/redis/` | rueidis fx module, distributed locks, pub/sub | redis/rueidis |
| `cache/singleflight/` | typed de-dupe for concurrent identical reads | golang.org/x/sync |

### Observability

| Module | Purpose | Key dep |
|---|---|---|
| `otel/` | full OTel SDK — tracer + meter + logs + OTLP exporter | go.opentelemetry.io/otel |
| `observability/sentry/` | Sentry fx module, slog/OTel bridged | getsentry/sentry-go |
| `observability/profiling/` | Pyroscope continuous profiling (in-process + eBPF mode) | grafana/pyroscope-go |
| `observability/pprof/` | auth-gated `/debug/pprof` endpoint | stdlib |
| `observability/statuspage/` | public `/status` page — uptime + dependency health | custom |

### Kubernetes runtime

| Module | Purpose | Key dep |
|---|---|---|
| `k8s/podinfo/` | downward-API env → fx-provided `PodInfo` | stdlib |
| `k8s/health/` | `/livez` `/readyz` `/startupz` backed by tagged check registry | stdlib |
| `k8s/metrics/prom/` | Prometheus `/metrics` + per-check-status gauges | prometheus/client_golang |
| `k8s/client/` | client-go — in-cluster + kubeconfig + GKE/EKS/Azure workload identity | k8s.io/client-go |
| `container/runtime/` | detect runtime (k8s / docker / podman / systemd / bare) + unified Info | stdlib |
| `leader/` | pluggable leader-election interface + Callbacks | — |
| `leader/k8s/` | Kubernetes Lease backend | k8s.io/client-go |
| `leader/pg/` | PostgreSQL advisory-lock backend | jackc/pgx/v5 |
| `systemd/` | `sd_notify` + watchdog (no-op when `NOTIFY_SOCKET` unset) | stdlib |

### Notifications & realtime

| Module | Purpose | Key dep |
|---|---|---|
| `notify/` | unified `Sender` + `Notifier` (first-success / fan-out) + SMTP | wneessen/go-mail |
| `notify/resend/` `notify/postmark/` `notify/sendgrid/` `notify/mailgun/` `notify/ses/` | transactional email senders | per-provider SDK |
| `notify/twilio/` | SMS / WhatsApp via Twilio | twilio/twilio-go |
| `notify/fcm/` | Firebase Cloud Messaging push | firebase.google.com/go/v4 |
| `notify/apns2/` | Apple Push Notification Service | sideshow/apns2 |
| `notify/webpush/` | RFC 8030 Web Push (browser) | SherClockHolmes/webpush-go |
| `notify/telegram/` | Telegram bot sender | tucnak/telebot/v3 |
| `notify/teams/` | Microsoft Teams Adaptive Card sender | atc0005/go-teams-notify/v2 |
| `notify/discord/` `notify/slack/` | webhook senders (no SDK — raw HTTP) | stdlib |
| `notify/unsub/` | RFC 8058 one-click unsubscribe + suppression list | custom |
| `notify/bounce/` | SES / Postmark bounce + complaint webhook handlers | custom |
| `realtime/sse/` | Server-Sent Events hub | r3labs/sse |
| `realtime/pubsub/` | pub/sub abstraction — pg LISTEN/NOTIFY, redis, NATS | custom |

### Webhooks

| Module | Purpose | Key dep |
|---|---|---|
| `webhooks/out/` | outbound delivery — HMAC sign + exponential retry + dead-letter + replay | custom |
| `webhooks/in/` | inbound signature verification — Stripe, GitHub, Slack, generic HMAC | stdlib |

### SaaS primitives

| Module | Purpose | Key dep |
|---|---|---|
| `tenancy/` | tenant context middleware, header + subdomain extractors | custom |
| `idempotency/` | `Idempotency-Key` middleware with pluggable store | custom |
| `flags/` | typed feature flags, OpenFeature-compatible provider interface | open-feature/go-sdk |
| `audit/` | append-only audit event log with Diff | custom on pgx |
| `page/` | typed cursor + offset pagination for sqlc/ogen | custom |

### Files / storage / media

| Module | Purpose | Key dep |
|---|---|---|
| `storage/` | `Bucket` interface + local FS backend | custom |
| `storage/presign/` | S3 direct-browser upload helpers | aws/aws-sdk-go-v2 |
| `storage/tus/` | resumable uploads (tus protocol) | tus/tusd |
| `storage/safety/` | EXIF strip + SSRF guards + path-traversal protection | custom |
| `storage/scan/` | ClamAV malware scan for uploads | custom |
| `archive/` | zip / tar / rar / 7z / brotli / zstd extract + create | mholt/archives |
| `media/av/` | FFmpeg probe + transcode (CGO sub-module) | asticode/go-astiav |
| `media/img/` | image resize + convert + optimize (CGO sub-module) | davidbyttow/govips/v2 |
| `media/cv/` | face detection, object detection, video thumbnails (CGO sub-module) | hybridgroup/gocv |
| `media/audio/` | audio decode / encode / analyse | faiface/beep |
| `ocr/` | text extraction from images + PDFs (CGO sub-module, own go.mod) | otiai10/gosseract |
| `pdf/` | HTML → PDF via headless Chrome | chromedp/chromedp |
| `pdf/parse/` | PDF metadata + validation + merge + optimize (pure Go) | pdfcpu/pdfcpu |
| `docs/xlsx/` | XLSX read + write | xuri/excelize/v2 |
| `docs/docx/` | DOCX template substitution (body / header / footer) | nguyenthenguyen/docx |
| `docs/epub/` | EPUB 3.0 generator | bmaupin/go-epub |
| `markdown/` | Markdown → HTML (GFM) | yuin/goldmark |
| `htmltmpl/` | type-safe HTML templates | a-h/templ |
| `jsonschema/` | JSON Schema 2020-12 validation | santhosh-tekuri/jsonschema |
| `hash/` | SHA-256, BLAKE3, xxhash-64, ETag helpers | cespare/xxhash + zeebo/blake3 |
| `fs/watch/` | recursive directory watch with debounce | fsnotify/fsnotify |

### Search & AI

| Module | Purpose | Key dep |
|---|---|---|
| `search/` | `Indexer`/`Searcher` interface + MemorySearcher | custom |
| `ai/llm/` | unified Chat / Stream / Embed interface — Anthropic, OpenAI, Ollama | anthropics/anthropic-sdk-go |
| `ai/vector/` | pgvector schema helpers, embedding store, similarity search, hybrid search | pgvector/pgvector-go |

### Commerce

| Module | Purpose | Key dep |
|---|---|---|
| `payments/stripe/` | Stripe Checkout + Portal + Payment Intents + webhook verify | stripe/stripe-go |
| `payments/subs/` | provider-agnostic subscription state machine | custom |
| `payments/meter/` | usage metering with idempotency + billing export | custom |
| `payments/invoice/` | PDF invoicing with sequential numbering | uses pdf/ + storage/ |
| `money/` | currency-aware minor-unit Money type, ISO 4217 | Rhymond/go-money |

### Integrations

| Module | Purpose | Key dep |
|---|---|---|
| `geoip/` | MaxMind GeoLite2 country / city / ASN lookups | oschwald/maxminddb-golang |
| `secrets/` | `Secret` interface + env / file / static backends | custom |
| `integrations/goenvoy/` | fx adapter for `github.com/golusoris/goenvoy` typed HTTP clients | github.com/golusoris/goenvoy |

### Big alternative stacks (opt-in)

| Module | Purpose | Key dep |
|---|---|---|
| `grpc/` | gRPC server + `ConnFactory` — OTel, slog logging, panic recovery, keepalive | grpc/grpc-go |
| `graphql/` | gqlgen server — GET/POST/SSE/WebSocket, APQ, complexity limit, GraphiQL | 99designs/gqlgen |
| `graphql/client/` | genqlient typed GraphQL client — auth transport, WebSocket opt-in | Khan/genqlient |
| `pubsub/kafka/` | Kafka producer + consumer | twmb/franz-go |
| `pubsub/nats/` | NATS JetStream | nats-io/nats.go |
| `net/wol/` | Wake-on-LAN magic-packet sender (stdlib only) | custom |
| `net/dnsserver/` | Authoritative + recursive DNS server — UDP + TCP, `*dns.ServeMux` | miekg/dns |
| `net/smtpserver/` | Inbound SMTP server, `HandlerBackend` callback API | emersion/go-smtp |
| `ebpf/` | cilium/ebpf scaffold — `ObjectProvider` + `Registry[Loader]` | cilium/ebpf |
| `deploy/crossplane/` | Crossplane XRD + Composition YAML (AWS RDS + ElastiCache) | — |

### Specialty sub-modules (own `go.mod`)

Heavy / CGO / native-dep packages each live in their own `go.mod` so the main framework's dep graph stays lean.

| Sub-module | Purpose | Key dep |
|---|---|---|
| `science/numerical/` | gonum linear algebra, statistics, optimization | gonum/gonum |
| `science/plot/` | chart rendering — line, scatter → PNG/file | gonum/plot |
| `science/bio/` | bioinformatics — FASTA parser, rev-complement, GC content | biogo/biogo |
| `web3/evm/` | Ethereum / EVM client, key generation, Wei↔Ether | ethereum/go-ethereum |
| `web3/solana/` | Solana RPC client, keypair, lamport helpers | gagliardetto/solana-go |
| `hw/gpio/` | GPIO output, I²C bus, SPI port | periph.io/x/conn/v3 |
| `hw/robotics/` | gobot Master / Robot scaffold for drones + arduinos | gobot.io/x/gobot/v2 |
| `hw/udev/` | Linux udev device event monitor channel | jochenvg/go-udev |
| `hw/fssnap/` | ZFS + Btrfs snapshot helpers (wraps CLI, stdlib only) | custom |
| `media/game/` | Ebitengine 2D game loop scaffold | hajimehoshi/ebiten/v2 |
| `media/3d/` | g3n 3D engine scaffold | g3n/engine |
| `testutil/pact/` | Pact consumer-driven contract testing | pact-foundation/pact-go/v2 |

### Misc utilities

| Module | Purpose | Key dep |
|---|---|---|
| `clikit/` | cobra + fx-aware CLI app builder | spf13/cobra |
| `clikit/tui/` | bubbletea `Run` / `RunInline` helpers | charmbracelet/bubbletea |
| `selfupdate/` | binary self-update from GitHub releases with SHA-256 verification | minio/selfupdate |
| `plugin/` | generic thread-safe extension-point `Registry[T]` | custom |

### Testing utilities

| Module | Purpose | Key dep |
|---|---|---|
| `testutil/pg/` | testcontainers PostgreSQL | testcontainers-go |
| `testutil/redis/` | testcontainers Redis | testcontainers-go |
| `testutil/river/` | in-process river test harness with real Postgres | riverqueue/river |
| `testutil/fxtest/` | fx lifecycle helpers for unit tests | go.uber.org/fx/fxtest |
| `testutil/snapshot/` | golden-file / snapshot testing | gkampitakis/go-snaps |
| `testutil/factory/` | deterministic gofakeit test data factories | brianvoe/gofakeit |
| `testutil/fuzz/` | fuzz corpus directory helpers + round-trip assertion | stdlib |
| `testutil/load/` | vegeta load-test harness — `Attack`, `Assert`, `MaxP99` | tsenart/vegeta |
| `testutil/mutation/` | go-mutesting runner + score assertion | avito-tech/go-mutesting |
| `testutil/prop/` | property-based testing (own go.mod) | leanovate/gopter |
| `testutil/pact/` | Pact contract testing (own go.mod) | pact-foundation/pact-go/v2 |

### CLI binaries

| Binary | Purpose |
|---|---|
| `cmd/golusoris` | scaffolder: `golusoris init`, `add <module>`, `bump <version>` with codemods |
| `cmd/golusoris-mcp` | MCP JSON-RPC server — exposes framework tools to MCP clients (Claude, Cursor, …) |

### Deploy artifacts

| Path | Purpose |
|---|---|
| `deploy/helm/` | base Helm chart — Deployment, Service, HPA, PDB, NetworkPolicy, CiliumNetworkPolicy, ServiceMonitor, backup CronJob |
| `deploy/observability/` | PrometheusRule (5 alerts) + Grafana dashboard (request rate, error rate, P99 latency) |
| `deploy/logging/` | Loki + Promtail config for structured log collection |
| `deploy/terraform/` | Terraform modules — VPC, RDS, ElastiCache, S3, IAM |
| `deploy/flux/` | Flux GitOps manifests (HelmRelease + ImageUpdateAutomation) |
| `deploy/argocd/` | Argo CD Application manifests |
| `deploy/crossplane/` | Crossplane XRD + Composition (AWS RDS + ElastiCache) + claim example |

---

## Tooling

```sh
make ci          # golangci-lint + govulncheck + gosec + go test -race
make lint        # golangci-lint only
make test        # go test -race -count=1 ./...
make sec         # govulncheck + gosec
```

### Scaffolding

```sh
golusoris init my-service          # generate a minimal new app
golusoris add grpc                 # wire grpc module into existing app
golusoris add auth/oidc
golusoris bump v0.5.0              # apply codemods for the new version
```

---

## Status

Pre-alpha (`v0.0.x`). All modules above are committed; public API may change before `v0.1.0`.

---

## License

[MIT](LICENSE).

## Support

If golusoris saves you time, a coffee helps ☕

<p align="left">
  <a href="https://ko-fi.com/lusoris" target="_blank">
    <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="Support me on Ko-fi" />
  </a>
  &nbsp;&nbsp;
  <a href="https://github.com/sponsors/lusoris" target="_blank">
    <img src="https://img.shields.io/badge/Sponsor-%E2%9D%A4-ea4aaa?style=for-the-badge&logo=github&logoColor=white" alt="GitHub Sponsors" />
  </a>
</p>
