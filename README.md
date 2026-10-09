<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# golusoris

[![HISS-21 lattice](https://img.shields.io/badge/Standards-Praetor%20HISS--21%20lattice-brightgreen)](AGENTS.md)

[![Release](https://img.shields.io/github/v/release/golusoris/golusoris?display_name=tag&sort=semver)](https://github.com/golusoris/golusoris/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/golusoris/golusoris.svg)](https://pkg.go.dev/github.com/golusoris/golusoris)
[![Go Report Card](https://goreportcard.com/badge/github.com/golusoris/golusoris)](https://goreportcard.com/report/github.com/golusoris/golusoris)
[![Go Version](https://img.shields.io/github/go-mod/go-version/golusoris/golusoris)](go.mod)
[![CI](https://github.com/golusoris/golusoris/actions/workflows/ci.yml/badge.svg)](https://github.com/golusoris/golusoris/actions/workflows/ci.yml)
[![Release Build](https://github.com/golusoris/golusoris/actions/workflows/release.yml/badge.svg)](https://github.com/golusoris/golusoris/actions/workflows/release.yml)
[![SBOM](https://github.com/golusoris/golusoris/actions/workflows/sbom.yml/badge.svg)](https://github.com/golusoris/golusoris/actions/workflows/sbom.yml)
[![CodeQL](https://github.com/golusoris/golusoris/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/golusoris/golusoris/actions/workflows/github-code-scanning/codeql)
[![Docs](https://github.com/golusoris/golusoris/actions/workflows/docs.yml/badge.svg)](https://golusoris.github.io/golusoris/)
[![Code: EUPL-1.2](https://img.shields.io/badge/code-EUPL--1.2-315c9b.svg)](LICENSING.md)
[![Docs: CC BY-SA 4.0](https://img.shields.io/badge/docs-CC%20BY--SA%204.0-b85c00.svg)](LICENSING.md)
[![REUSE compliant](https://img.shields.io/badge/REUSE-compliant-green.svg)](https://reuse.software/)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/golusoris/golusoris/badge)](https://scorecard.dev/viewer/?uri=github.com/golusoris/golusoris)
[![ko-fi](https://img.shields.io/badge/ko--fi-support-FF5E5B?logo=ko-fi&logoColor=white)](https://ko-fi.com/lusoris)

A composable Go framework built around [`go.uber.org/fx`](https://github.com/uber-go/fx). Pick the modules your app needs — nothing else ships. Every module follows the same [principles](docs/principles.md): Power-of-10 coding rules, SEI CERT secure-coding, Google Go Style, RFC 9457 error bodies, OTel SemConv v1.26, and evidence-backed supply-chain controls.

**Documentation:** <https://golusoris.github.io/golusoris/> — the mkdocs site built from
[`docs/`](docs/index.md) on every push to `main` by
[`docs.yml`](.github/workflows/docs.yml).

---

## Quick start

```go
import (
    "github.com/golusoris/golusoris"
    "github.com/golusoris/golusoris/otel"
)

fx.New(
    golusoris.Core,       // config · log · clock · id · validate · crypto
    golusoris.DB,         // pgx pool · migrate
    otel.Module,          // tracer · meter · logs · OTLP exporter
    golusoris.HTTP,       // chi router · HTTP server
    golusoris.K8s,        // pod metadata · Kubernetes client
    golusoris.Jobs,       // river client · worker registry
    golusoris.CacheRedis, // rueidis distributed cache
    // ... add what you need
).Run()
```

The default `core/config.Module` reads `APP_` environment variables through
koanf. Applications can opt into YAML or JSON files; packages without
configuration expose constructors or options directly.

---

## Principles

The full contract is in [docs/principles.md](docs/principles.md). Short form:

| # | Rule |
| --- | --- |
| **Coding** | NASA/JPL Power of 10 adapted for Go — no `goto`, bounded loops, ≤60-line functions, every error checked, no unreviewed lint/gosec/reachable-vulnerability finding on merge |
| **Security** | SEI CERT for Go — safe crypto, input validation at every boundary, no `unsafe` outside reviewed hot-paths |
| **Style** | Google Go Style Guide (canonical) + Effective Go (secondary) |
| **Architecture** | C4 diagrams in `docs/architecture/`; Nygard ADRs in `docs/adr/` |
| **Supply chain** | SBOM, cosign signing, and build-provenance attestations on every release |
| **Compliance** | No blanket certification; apps map and verify their own OWASP, NIST, EU, BSI, NCSC, privacy, and AI controls |
| **APIs** | RFC 9457 Problem Details · OpenAPI 3.1 · OTel SemConv v1.26 · JWT/OAuth 2.1/PKCE/WebAuthn |
| **Testing** | Table-driven tests · `-race` on every CI run · 70% coverage (85% security-critical) · real containers over mocks |
| **Deployment** | Twelve-Factor · CNCF cloud-native · OCI multi-arch · rootless + read-only FS |

Every merged commit: **no unreviewed lint, gosec, or reachable-vulnerability
finding · race-green.** Exact vulnerability exceptions bind the module
checksum and patched-source hash, then fail closed on drift.

---

## Module catalog

### Core

`core/` is its own Go module (`github.com/golusoris/golusoris/core`, ADR-0017): ~20 direct dependencies, importable by governance tools and small CLIs without the root module's graph. The capability contract every package participates in lives in [`capabilities.yaml`](capabilities.yaml).

| Module | Purpose | Key dep |
| --- | --- | --- |
| `core/config/` | koanf v2 — env + file + YAML, Kubernetes secret dirs, `*_FILE` env indirection, file-watch (ConfigMap hot-reload), SIGHUP hook | knadh/koanf/v2 |
| `core/codec/yaml/` | fleet YAML codec — strict, bounded, atomic writes | go.yaml.in/yaml/v3 |
| `core/codec/jcs/` | RFC 8785 JSON canonicalisation for hashed/signed records — UTF-16 member order, ECMAScript numbers, refuses duplicate names, lone surrogates, invalid UTF-8 | stdlib |
| `core/log/` | slog factory: tint (dev) / JSON (prod), podinfo attrs, OTel bridge | lmittmann/tint |
| `core/errors/` | typed error codes, HTTP status mapping, RFC 9457 responses | go-faster/errors |
| `core/crypto/` | argon2id passwords, AES-GCM helpers, bounded configured encryptor | alexedwards/argon2id |
| `core/crypto/receipt/` | Ed25519 Exit-0 execution receipts (praetor-compatible) | stdlib |
| `core/clock/` | mockable wall clock (real + fake) — `time.Now()` is banned outside this package | jonboulle/clockwork |
| `core/id/` | UUIDv7 and KSUID generators | google/uuid · segmentio/ksuid |
| `core/retry/` | `Do(ctx, fn, Policy, clock)`: capped exponential backoff, jitter, retryable predicate, context-bounded waits | stdlib |
| `core/validate/` | go-playground/validator wrapper with i18n error messages | go-playground/validator/v10 |
| `core/version/` | build metadata (ldflags / VCS) as a typed `Info` | stdlib |
| `core/clikit/` | cobra + fx CLI builder; bash/zsh/fish/PowerShell completions, man pages, generated-file drift check (`clikit/tui` bubbletea helpers stay in the root module) | spf13/cobra |
| `core/mcp/` | MCP server fx module — stdio + streamable-HTTP | modelcontextprotocol/go-sdk |
| `core/gitx/` | bounded git runner + `worktree/` per-task worktrees | stdlib |
| `core/astx/` | source walker, AST import rewriter (codemods), func metrics, go.mod reader | golang.org/x/mod |
| `core/capabilities/` | schema + loader for the root `capabilities.yaml` contract | — |
| `core/tlsx/` | file-backed TLS: cert/key/CA reloaded lazily on handshake or loaded once into a client config, client-auth policy parsing; `tlsxtest/` issues throwaway test certificates | stdlib |
| `i18n/` | locale negotiation middleware, message catalog | nicksnyder/go-i18n |

### Database & data

| Module | Purpose | Key dep |
| --- | --- | --- |
| `db/pgx/` | pgx pool fx module + startup retry + slow-query logger; CloudNativePG password/cert files re-read per connection, read-only `ReadPool` | jackc/pgx/v5 |
| `db/bun/` | bun ORM fx module over the shared pgx pool | uptrace/bun |
| `db/sqlite/` | embedded SQLite (modernc, pure Go) fx module — WAL + foreign keys on by default | modernc.org/sqlite |
| `db/migrate/` | golang-migrate v4 runner + fx lifecycle hook | golang-migrate/migrate/v4 |
| `db/migrate/sqlite/` | the same runner for SQLite (pure-Go driver, pragmas of `db/sqlite`) | golang-migrate/migrate/v4 |
| `db/sqlc/` | shared sqlc.yaml fragment + query helpers | sqlc-dev/sqlc |
| `db/geo/` | Point EWKB scanner, EWKT value, and Haversine distance | custom on pgx |
| `db/timescale/` | TimescaleDB edition detection (Apache vs community), hypertables + chunk interval, compression, retention, continuous aggregates, fx probe | custom on pgx |
| `db/clickhouse/` | ClickHouse OLAP client fx module | ClickHouse/clickhouse-go/v2 |
| `db/cdc/` | PostgreSQL logical-replication (WAL) consumer — pgoutput decoder → `Event` | jackc/pglogrepl |
| `outbox/` | transactional outbox — write events in same tx, drain via river; CloudEvents envelope (stable id, trace context, tenant) | custom on pgx |
| `outbox/cdc/` | CDC-based outbox drain → Kafka / NATS / GCP / Webhook sinks; CloudEvents sinks for NATS JetStream + Kafka | uses db/cdc |

### HTTP / API

| Module | Purpose | Key dep |
| --- | --- | --- |
| `httpx/server/` | `*http.Server` with slow-loris guards, body limits, graceful shutdown, file-based TLS/mTLS reloaded on rotation | stdlib |
| `bootstrap/` | lean service entry point: Core + HTTP groupings without the rest of the framework | — |
| `httpx/router/` | chi router + http.Handler provided to fx graph | go-chi/chi |
| `httpx/middleware/` | logger, recovery, request-id, OTel, secure-headers, compress, ETag, trust-proxy | composite |
| `httpx/client/` | retry + circuit-breaker + OTel-instrumented HTTP client; custom TLS config or transport | sony/gobreaker |
| `httpx/extclient/` | typed, bounded external-API client over the resilient HTTP transport | custom on httpx/client |
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
| `httpx/inertia/` | Inertia.js v2 server adapter (middleware + render) for chi | romsar/gonertia/v3 |
| `ogenkit/` | ogen server adapter, RFC 9457 error mapper, middleware glue | ogen-go/ogen |
| `apidocs/` | Scalar UI (`/docs`) + opt-in authenticated MCP-from-OpenAPI (`/mcp`) | Scalar (JS, embedded) |

### Auth & identity

| Module | Purpose | Key dep |
| --- | --- | --- |
| `auth/oidc/` | OIDC + PKCE + session storage | coreos/go-oidc/v3 |
| `auth/passkeys/` | WebAuthn + TOTP (MFA) | go-webauthn/webauthn + pquerna/otp |
| `auth/jwt/` | HMAC JWT issuance, expiry, and validation | golang-jwt/jwt/v5 |
| `auth/apikey/` | HMAC API key issuance, verification, and scopes | custom |
| `auth/magiclink/` | passwordless email-link sign-in | custom |
| `auth/linking/` | multi-IdP identity linking per account | custom |
| `auth/impersonate/` | audited admin impersonation + banner + auto-revert | custom |
| `auth/session/` | cookie session manager, in-memory store, pluggable Store SPI | custom |
| `auth/recovery/` | recovery codes + forgot-password flow | custom |
| `auth/policy/` | password strength (zxcvbn) + breach check (HIBP k-anon) | nbutton23/zxcvbn-go |
| `auth/lockout/` | per-identity login rate-limit + cooldown | custom Store SPI |
| `auth/oauth2server/` | OAuth 2.1 authorization-code + PKCE token issuer (not OIDC) | custom on auth/jwt |
| `auth/scim/` | SCIM 2.0 user + group provisioning endpoint | custom |
| `auth/captcha/` | Cloudflare Turnstile + hCaptcha + reCAPTCHA verifier middleware | custom |
| `authz/` | RBAC / ABAC policy enforcement | casbin/casbin/v3 |

### Background work

| Module | Purpose | Key dep |
| --- | --- | --- |
| `jobs/` | river client + worker registry + named queues + lifecycle observer + drain/retry + depth metrics | riverqueue/river, rivercontrib/otelriver |
| `jobs/sqlite/` | River queue on SQLite for standalone single-binary mode | riverqueue/river/riverdriver/riversqlite |
| `jobs/cron/` | cron expression parser / validator | robfig/cron/v3 |
| `jobs/ui/` | auth-gated river job dashboard handler | riverqueue/riverui |
| `jobs/workflow/` | Temporal workflow orchestration | go.temporal.io/sdk |

### Caching

| Module | Purpose | Key dep |
| --- | --- | --- |
| `cache/memory/` | typed in-memory L1 cache (TinyLFU eviction) | maypok86/otter/v2 |
| `cache/redis/` | rueidis fx module, distributed locks, pub/sub | redis/rueidis |
| `cache/singleflight/` | typed de-dupe for concurrent identical reads | golang.org/x/sync |
| `cache/twotier/` | typed L1 memory + optional Redis L2 cache, bounded prefix invalidation, cross-replica L1 eviction | custom on memory + redis |

### Observability

| Module | Purpose | Key dep |
| --- | --- | --- |
| `otel/` | full OTel SDK — tracer + meter + logs + OTLP exporter | go.opentelemetry.io/otel |
| `observability/sentry/` | Sentry fx module + slog event/breadcrumb bridge | getsentry/sentry-go |
| `observability/profiling/` | in-process Pyroscope continuous profiling | grafana/pyroscope-go |
| `observability/pprof/` | auth-gated `/debug/pprof` endpoint | stdlib |
| `observability/statuspage/` | public `/status` page — uptime + dependency health | custom |
| `observability/metricdef/` | one metric catalog for services, dashboards, rules + checks; typed handles, cardinality guard, exemplars | prometheus/client_golang |
| `observability/grafana/` | Grafana dashboards generated from metricdef defs — rate/quantile/stat panels, variables, annotations, links, units, thresholds | grafana/grafana-foundation-sdk |
| `observability/rules/` | PrometheusRule + promtool rule files with mandatory runbook URLs; multi-window multi-burn-rate SLO alerts | prometheus-operator/prometheus-operator (apis) |

### Kubernetes runtime

| Module | Purpose | Key dep |
| --- | --- | --- |
| `k8s/podinfo/` | downward-API env → fx-provided `PodInfo` | stdlib |
| `k8s/health/` | `/livez` `/readyz` `/startupz` backed by tagged check registry; shutdown gate fails readiness and drains before servers stop | stdlib |
| `k8s/metrics/prom/` | Prometheus `/metrics` + per-check-status gauges | prometheus/client_golang |
| `k8s/keda/` | KEDA external scaler gRPC over jobs queue depth (scale to zero) | google.golang.org/grpc, KEDA externalscaler.proto |
| `k8s/client/` | client-go — in-cluster + kubeconfig + GKE/EKS/Azure workload identity | k8s.io/client-go |
| `k8s/operator/` | controller-runtime manager fx module + application-supplied CRD schemes | sigs.k8s.io/controller-runtime |
| `k8s/cnpg/` | CloudNativePG backup health check — last backup age, failed backup, WAL archiving (dynamic client, no CNPG import) | k8s.io/client-go |
| `k8s/dra/` | DRA ResourceSlice publisher — node devices with attributes + capacity, update-on-change, cleanup on stop | k8s.io/dynamic-resource-allocation |
| `k8s/nfd/` | Node Feature Discovery local feature files — atomic write, label validation, expiring refresh | k8s.io/apimachinery |
| `k8s/nri/` | containerd NRI plugin scaffold — typed pod/container lifecycle hooks, context-timeout bounded (own go.mod) | containerd/nri |
| `container/runtime/` | detect runtime (k8s / docker / podman / systemd / bare) + unified Info | stdlib |
| `leader/` | pluggable leader-election interface + Callbacks + `Status.IsLeader()`; `NamedModule` per singleton task | — |
| `leader/always/` | always-leader backend for standalone single-replica runs | — |
| `leader/k8s/` | Kubernetes Lease backend | k8s.io/client-go |
| `leader/pg/` | PostgreSQL advisory-lock backend | jackc/pgx/v5 |
| `systemd/` | `sd_notify` + watchdog (no-op when `NOTIFY_SOCKET` unset) | stdlib |

### Notifications & realtime

| Module | Purpose | Key dep |
| --- | --- | --- |
| `notify/` | unified `Sender` + `Notifier` (first-success / fan-out) + SMTP | wneessen/go-mail |
| `notify/resend/` `notify/postmark/` `notify/sendgrid/` `notify/mailgun/` | transactional email senders | raw HTTP |
| `notify/twilio/` | SMS / WhatsApp via Twilio | raw HTTP |
| `notify/fcm/` | Firebase Cloud Messaging push | raw HTTP + golang-jwt/jwt/v5 |
| `notify/apns2/` | Apple Push Notification Service | raw HTTP/2 + golang-jwt/jwt/v5 |
| `notify/webpush/` | RFC 8030 Web Push (browser) | SherClockHolmes/webpush-go |
| `notify/telegram/` | Telegram bot sender | raw HTTP |
| `notify/teams/` | Microsoft Teams MessageCard sender | raw HTTP |
| `notify/gotify/` `notify/ntfy/` | bounded raw-HTTP push senders | stdlib |
| `notify/discord/` `notify/slack/` | webhook senders (no SDK — raw HTTP) | stdlib |
| `notify/inbound/` | verified SES/Postmark inbound-email normalization + bounded MIME parsing | custom |
| `notify/tracking/` | signed open-pixel and click tracking handlers | custom |
| `notify/unsub/` | RFC 8058 one-click unsubscribe + suppression list | custom |
| `notify/bounce/` | SES / Postmark bounce + complaint webhook handlers | custom |
| `realtime/sse/` | Server-Sent Events hub | r3labs/sse |
| `realtime/pubsub/` | synchronous local pub/sub bus plus Redis adapter | custom + rueidis |
| `realtime/webrtc/` | bounded Pion offer/answer signaling + data-channel hooks | pion/webrtc |

### Webhooks

| Module | Purpose | Key dep |
| --- | --- | --- |
| `webhooks/out/` | outbound delivery — HMAC sign + exponential retry + dead-letter + replay | custom |
| `webhooks/in/` | inbound signature verification — Stripe, GitHub, Slack, generic HMAC | stdlib |

### SaaS primitives

| Module | Purpose | Key dep |
| --- | --- | --- |
| `tenancy/` | tenant context middleware, header + subdomain extractors | custom |
| `idempotency/` | `Idempotency-Key` middleware + gRPC interceptor; memory, Postgres, Redis, SQLite stores | custom + pgx + rueidis |
| `flags/` | typed feature flags with an OpenFeature-shaped provider interface | custom |
| `audit/` | append-only audit event log with Diff + pluggable Store | custom |
| `page/` | typed cursor + offset pagination for sqlc/ogen | custom |

### Files / storage / media

| Module | Purpose | Key dep |
| --- | --- | --- |
| `storage/` | `Bucket` interface + local FS and S3 backends; presigned GET/PUT, multipart upload, server-side copy, STS role/web-identity credentials (GCS + Azure Blob: own modules below) | aws/aws-sdk-go-v2 |
| `storage/tus/` | resumable uploads (tus protocol) | tus/tusd |
| `storage/safety/` | Animation-safe raster metadata strip + SSRF guards + path-traversal protection + magic-byte content-type detection | code.dny.dev/ssrf + h2non/filetype + stdlib |
| `storage/scan/` | ClamAV malware scan for uploads (fail-closed) | baruwa-enterprise/clamd |
| `archive/` | zip / tar / rar / 7z / brotli / zstd extract + create + recursive dir copy | mholt/archives + otiai10/copy |
| `media/av/` | probe/transcode interfaces; no runtime backend bundled | application-provided |
| `media/img/` | image-processing interfaces; no runtime backend bundled | application-provided |
| `media/img/pipeline/` | bounded signed-URL resize orchestration over an injected processor | stdlib crypto/hmac |
| `media/cv/` | computer-vision interfaces; no runtime backend bundled | application-provided |
| `media/audio/` | audio decode + analyse — duration, waveform, LUFS loudness (own go.mod, pure-Go, no CGO) | go-mp3 + mewkiz/flac + oggvorbis + ebur128 |
| `ocr/` | text extraction from images + PDFs (CGO sub-module, own go.mod) | otiai10/gosseract |
| `pdf/` | HTML → PDF via headless Chrome | chromedp/chromedp |
| `pdf/parse/` | PDF metadata + validation + merge + optimize (pure Go) | pdfcpu/pdfcpu |
| `docs/xlsx/` | XLSX read + write | xuri/excelize/v2 |
| `docs/docx/` | DOCX template substitution (body / header / footer) | nguyenthenguyen/docx |
| `docs/epub/` | EPUB 3.0 generator | bmaupin/go-epub |
| `markdown/` | Markdown → HTML (GFM) | yuin/goldmark |
| `htmltmpl/` | SSR HTML templates (auto-escaping) + opt-in helper seam | stdlib html/template + FuncProvider |
| `jsonschema/` | JSON Schema 2020-12 validation + generation from Go types; `GenerateConfig` emits Helm `values.schema.json` from koanf config structs | santhosh-tekuri/jsonschema + invopop/jsonschema |
| `hash/` | SHA-256, BLAKE3, xxhash-64, ETag helpers | cespare/xxhash + zeebo/blake3 |
| `fs/watch/` | recursive directory watch with debounce | fsnotify/fsnotify |
| `torrent/` | torrent-client abstraction (add/list/control), config-selected backend | transmissionrpc · go-qbittorrent · go-rtorrent |

### Search & AI

| Module | Purpose | Key dep |
| --- | --- | --- |
| `search/` | `Indexer`/`Searcher` interface + MemorySearcher | custom |
| `search/meilisearch/` | Meilisearch index/search backend | meilisearch-go |
| `search/typesense/` | Typesense index/search backend | typesense-go/v2 |
| `search/pgfts/` | PostgreSQL full-text search over application-owned tables | custom on pgx |
| `ai/llm/` | unified Chat / Stream / Embed interface — Anthropic, OpenAI, Ollama | raw HTTP |
| `ai/vector/` | pgvector schema helpers, embedding store, similarity search, hybrid search | pgvector/pgvector-go |
| `ai/tiny/` | bounded trainer/predictor contracts, registries, and container runner | custom + pgx |
| `ai/tiny/gemma/` `ai/tiny/litert/` | digest-pinned Gemma LoRA and MobileNet V2 classifier trainer orchestration | containerized KerasHub / TensorFlow |
| `ai/tiny/serve/` | Ollama + distributed fleet adapters; LiteRT sidecar client/protocol ([runtime #564](https://github.com/golusoris/golusoris/issues/564)) | HTTP + river |

### Commerce

| Module | Purpose | Key dep |
| --- | --- | --- |
| `payments/stripe/` | Stripe Checkout + Portal + Payment Intents + webhook verify | stripe/stripe-go |
| `payments/subs/` | provider-agnostic subscription state machine | custom |
| `payments/meter/` | usage metering with idempotency + billing export | custom |
| `payments/invoice/` | invoice model, sequential numbering, and HTML rendering | stdlib + money |
| `money/` | currency-aware minor-unit Money type, ISO 4217 | custom |

### Integrations

| Module | Purpose | Key dep |
| --- | --- | --- |
| `geoip/` | MaxMind GeoLite2 country / city / ASN lookups | oschwald/maxminddb-golang |
| `secrets/` | `Secret` interface + env / file / static backends | custom |
| `integrations/goenvoy/` | fx adapter for `github.com/golusoris/goenvoy` typed HTTP clients | github.com/golusoris/goenvoy |

### Big alternative stacks (opt-in)

| Module | Purpose | Key dep |
| --- | --- | --- |
| `grpc/` | gRPC server + `ConnFactory` — OTel, slog logging, panic recovery, configurable keepalive, mTLS with cert rotation, readiness-fed `grpc.health.v1`; client TLS, keepalive, and UNAVAILABLE retry policy from config | grpc/grpc-go |
| `graphql/` | gqlgen server — GET/POST/SSE/WebSocket, APQ, complexity limit, GraphiQL | 99designs/gqlgen |
| `graphql/client/` | genqlient typed GraphQL client — auth transport, WebSocket opt-in | Khan/genqlient |
| `pubsub/cloudevents/` | CloudEvents 1.0 envelope — validation, JSON event format, binary-mode header codecs | custom (stdlib) |
| `pubsub/gcp/` | Google Cloud Pub/Sub publisher + subscriber | cloud.google.com/go/pubsub/v2 |
| `pubsub/kafka/` | Kafka producer + consumer — TLS CA, SASL PLAIN/SCRAM, CloudEvents records | twmb/franz-go |
| `pubsub/nats/` | NATS JetStream — creds/NKey/TLS auth, CloudEvents publish with `Nats-Msg-Id` dedupe | nats-io/nats.go |
| `net/wol/` | Wake-on-LAN magic-packet sender (stdlib only) | custom |
| `net/dnsserver/` | Authoritative + recursive DNS server — UDP + TCP, `*dns.ServeMux` | miekg/dns |
| `net/smtpserver/` | Inbound SMTP server, `HandlerBackend` callback API | emersion/go-smtp |
| `ebpf/` | cilium/ebpf scaffold — `ObjectProvider` + `Registry[Loader]` | cilium/ebpf |
| `pkg/sockmap/` | opt-in eBPF SK_MSG/SOCKMAP acceleration for colocated TCP peers | cilium/ebpf |
| `deploy/crossplane/` | Crossplane XRD + Composition YAML (AWS RDS + ElastiCache) | — |

### Specialty sub-modules (own `go.mod`)

Heavy / CGO / native-dep packages each live in their own `go.mod` so the main framework's dep graph stays lean.

| Sub-module | Purpose | Key dep |
| --- | --- | --- |
| `container/registry/` | OCI/Docker registry client — resolve, manifest, tags, copy; OCI 1.1 artifact push/pull by digest, referrers | google/go-containerregistry |
| `container/registry/credentials/` | registry credential chain behind `authn.Keychain` — secret files, cloud workload identity, docker config | google/go-containerregistry |
| `container/registry/credentials/ecr/` | ECR credentials via AWS default chain (IRSA, Pod Identity) | aws/aws-sdk-go-v2/service/ecr |
| `container/registry/credentials/gar/` | Artifact Registry credentials via Application Default Credentials | golang.org/x/oauth2/google |
| `container/registry/credentials/acr/` | ACR credentials via Entra workload identity + token exchange | Azure/azure-sdk-for-go/sdk/azidentity |
| `container/registry/sign/` | in-process Sigstore signing by digest — key (KMS `crypto.Signer`) or keyless Fulcio, optional Rekor + TSA; bundle pushed as OCI 1.1 referrer that `cosign verify` accepts | sigstore/sigstore-go |
| `storage/gcs/` | Google Cloud Storage `storage.Bucket` — resumable upload, signed GET/PUT (key or IAM signBlob), server-side copy | cloud.google.com/go/storage |
| `storage/azblob/` | Azure Blob Storage `storage.Bucket` — staged block upload, SAS (shared key or user delegation), server-side copy; workload / managed identity | Azure/azure-sdk-for-go azblob + azidentity |
| `science/numerical/` | gonum linear algebra, statistics, optimization | gonum/gonum |
| `science/plot/` | chart rendering — line, scatter → PNG/file | gonum/plot |
| `science/bio/` | bounded FASTA parser, rev-complement, GC content | stdlib |
| `web3/evm/` | Ethereum / EVM client, key generation, Wei↔Ether | ethereum/go-ethereum |
| `web3/solana/` | Solana RPC client, keypair, lamport helpers | gagliardetto/solana-go |
| `hw/gpio/` | GPIO output, I²C bus, SPI port | periph.io/x/conn/v3 |
| `hw/robotics/` | gobot Master / Robot scaffold for drones + arduinos | gobot.io/x/gobot/v2 |
| `hw/udev/` | Linux udev device event monitor channel | jochenvg/go-udev |
| `hw/fssnap/` | ZFS + Btrfs snapshot helpers (wraps CLI, stdlib only) | custom |
| `media/game/` | Ebitengine 2D game loop scaffold | hajimehoshi/ebiten/v2 |
| `media/3d/` | g3n 3D engine scaffold | g3n/engine |
| `testutil/pact/` | Pact consumer-driven contract testing | pact-foundation/pact-go/v2 |
| `testutil/promcheck/` | dashboard + rule queries vs emitted metrics gate (own go.mod) | prometheus/prometheus promql/parser |

### Misc utilities

| Module | Purpose | Key dep |
| --- | --- | --- |
| `clikit/tui/` | bubbletea `Run` / `RunInline` helpers (the CLI builder itself is `core/clikit/`) | charmbracelet/bubbletea |
| `selfupdate/` | size-bounded binary self-update with authenticated publisher manifest and mandatory SHA-256 verification | minio/selfupdate |
| `plugin/` | generic thread-safe extension-point `Registry[T]` | custom |

### Testing utilities

| Module | Purpose | Key dep |
| --- | --- | --- |
| `testutil/pg/` | testcontainers PostgreSQL | testcontainers-go |
| `testutil/redis/` | testcontainers Redis | testcontainers-go |
| `testutil/clickhouse/` | testcontainers ClickHouse | testcontainers-go |
| `testutil/kafka/` | testcontainers Redpanda/Kafka endpoint | testcontainers-go |
| `testutil/nats/` | testcontainers NATS endpoint | testcontainers-go |
| `testutil/objstore/` | testcontainers object-storage emulators + shared `storage.Bucket` conformance suite | testcontainers-go |
| `testutil/river/` | in-process river test harness with real Postgres | riverqueue/river |
| `testutil/fxtest/` | fx lifecycle helpers for unit tests | go.uber.org/fx/fxtest |
| `testutil/snapshot/` | golden-file / snapshot testing | gkampitakis/go-snaps |
| `testutil/factory/` | deterministic gofakeit test data factories | brianvoe/gofakeit |
| `testutil/fuzz/` | fuzz corpus directory helpers + round-trip assertion | stdlib |
| `junit/` | JUnit XML report writer for CI gates — suites, pass/fail/error/skip, properties, XML-illegal characters escaped; validates against junit-10.xsd | stdlib |
| `testutil/fixture/` | typed CSV fixture loading — `Load`/`MustLoad` into struct slices | jszwec/csvutil |
| `testutil/load/` | vegeta load-test harness — `Attack`, `Assert`, `MaxP99` | tsenart/vegeta |
| `testutil/mutation/` | go-mutesting runner + score assertion | avito-tech/go-mutesting |
| `testutil/prop/` | property-based testing (own go.mod) | leanovate/gopter |
| `testutil/pact/` | Pact contract testing (own go.mod) | pact-foundation/pact-go/v2 |
| `testutil/ginkgofx/` | fx app lifecycle wired into Ginkgo BeforeSuite/AfterSuite (or BeforeEach/AfterEach) | onsi/ginkgo/v2 |

### CLI binaries

| Binary | Purpose |
| --- | --- |
| `cmd/golusoris` | scaffolder: `golusoris init`, `add <module>`, `bump <version>` with codemods |
| `cmd/golusoris-mcp` | MCP JSON-RPC server — exposes framework tools to MCP clients (Claude, Cursor, …) |

### Deploy artifacts

| Path | Purpose |
| --- | --- |
| `deploy/helm/` | base Helm chart — Deployment (preStop drain + validated termination grace), Service, HPA, PDB, NetworkPolicy, ServiceMonitor, backup CronJob |
| `deploy/observability/` | PrometheusRule (5 alerts) + Grafana dashboard (request rate, error rate, P99 latency) |
| `deploy/logging/` | Loki + Grafana Alloy config for structured log collection |
| `deploy/terraform/` | Terraform modules — AWS RDS PostgreSQL and S3 bucket |
| `deploy/pulumi/` | Pulumi AWS reference — VPC, RDS, Redis, ECS, and ALB |
| `deploy/multiregion/` | Pulumi active/passive AWS reference — Aurora Global Database, regional ECS, and Route53 failover |
| `deploy/flux/` | Flux GitOps manifests — HelmRepository, HelmRelease, and Kustomization; optional image automation is documented separately |
| `deploy/argocd/` | Argo CD Application manifests |
| `deploy/crossplane/` | Crossplane XRD + Composition (AWS RDS + ElastiCache) + claim example |

---

## Tooling

```sh
make verify-all  # universal gate: build/lint/security/race across all 27 Go modules plus governance and licensing
make ci          # golangci-lint + govulncheck + gosec + go test -race (current module)
make ci-all      # lint + govulncheck + gosec + race/coverage across all 27 modules
make lint        # golangci-lint only
make test        # go test -race -count=1 ./...
make sec         # govulncheck + gosec
make reuse-lint  # REUSE / SPDX compliance
```

Local git hooks run through [lefthook](lefthook.yml) (`lefthook install`); see
[CONTRIBUTING.md](CONTRIBUTING.md).

### Scaffolding

```sh
golusoris init my-service          # generate a minimal new app
golusoris add grpc                 # wire grpc module into existing app
golusoris add auth/oidc
golusoris bump v0.9.0              # go get + go mod tidy to that version; the core/ import-path rewrite is in docs/migrations/v0.9.0.md
```

---

## Status

Pre-1.0, actively developed. Latest tagged release: **v0.12.0** (root
module); the `core/` sub-module is at **core/v0.9.2**. Code has been
licensed under the [European Union Public Licence 1.2](LICENSE)
(`EUPL-1.2`) since the v0.9.0 relicense (ADR-0018) — v0.8.0 and earlier were
MIT; documentation is `CC-BY-SA-4.0`. GitHub immutable releases are enabled,
starting with [v0.10.1](https://github.com/golusoris/golusoris/releases/tag/v0.10.1):
`release.yml` runs goreleaser to publish archives, checksums, per-archive
SPDX SBOMs, cosign keyless signatures and build-provenance attestations. The
[v0.12.0 release run](https://github.com/golusoris/golusoris/actions/runs/35001126039)
published those release assets successfully. `sbom.yml` separately attempts
source-tree SPDX and CycloneDX attestations for each root tag; its
[v0.12.0 run](https://github.com/golusoris/golusoris/actions/runs/35001126102)
failed while writing to Rekor, so source-tree attestations are not claimed for
every tag. Governance runs on the
[praetor](https://github.com/cordanaLLM/praetor) HISS-21 lattice
(ADR-0019) — twenty-one invariants, from Acyclic Control Flow (HISS-01) to
Platform Neutrality (HISS-21); see [AGENTS.md](AGENTS.md) for the
full table and the gate that enforces each one in this repository.
Breaking changes between minor versions are called out in the commit
`Migration:` footer and in `docs/migrations/` — start with
[docs/migrations/v0.13.0.md](docs/migrations/v0.13.0.md) for current API and
secure-default changes. The [v0.9.0 guide](docs/migrations/v0.9.0.md) retains
the earlier `core/…` import-path mapping. Every module in the catalog above is
committed; CI gates all 27 discovered Go modules through the primary lane and
four deterministic module-sweep shards.

---

## License

Code is licensed under the [European Union Public Licence 1.2](LICENSE)
(`EUPL-1.2`); documentation under `CC-BY-SA-4.0`. The repository is
[REUSE](https://reuse.software)-compliant — see [LICENSING.md](LICENSING.md)
for the full split, third-party carve-outs, and the DCO contribution terms.

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

## Standards & Governance

This repository tracks the [praetor](https://github.com/cordanaLLM/praetor)
High-Integrity Systems Standards lattice — **HISS-21**: twenty-one invariants
spanning the modernized NASA JPL Power-of-10 rules (HISS-01 to HISS-15) and the
fleet rows HISS-16 to HISS-21 (Context Integrity, State Ledger Discipline,
Diff-Aware CI Efficiency, Reuse Before Writing, Enforcement Coverage, Platform
Neutrality). See
[AGENTS.md](AGENTS.md) for the full table: it names the gate that enforces each
invariant here. HISS-18 remains an explicit CI-efficiency gap; HISS-20 is wired
through the enforcement-coverage fixture catalogue; HISS-21 has a required
Linux, macOS, and Windows matrix after its first hosted green.

| Gate | Command | Description |
| :--- | :--- | :--- |
| **Verification** | `make verify-all` | Runs every declared local code, security, documentation, infrastructure, licensing, and governance gate |
| **HISS Audit** | `praetorctl audit` | Enforces zero technical debt regression against baseline |
| **Context Sync** | `praetorctl compile-context` | Transpiles canonical `AGENTS.md` to all AI targets |
| **Agent Register** | `make caveman-context` | Checks canonical package guides, personas, skills, and Paperclip surfaces |

<!-- praetor:readme-governance:start -->
[![Documentation Governance][praetor-docs-badge]][praetor-docs-runs]

Praetor manages this repository's declared governance policy. This managed
block records adoption state; it is not a verification certificate.

**Verification**: `make verify-all` runs the repository's configured
verification cascade.

**HISS Audit**: `praetorctl audit` enforces policy, generated-surface
integrity, and the debt ratchet.

**Context Sync**: `praetorctl compile-context --verify` verifies every
generated agent context against `AGENTS.md`.

**Documentation**: `make docs-lint` enforces locked Markdown style and the
private scratch-link policy.

**Debt Baseline**: `.standards-baseline.json` anchors the debt ratchet at
6 recorded infractions; audit forbids growth.

[praetor-docs-badge]: https://github.com/golusoris/golusoris/actions/workflows/praetor-docs.yml/badge.svg
[praetor-docs-runs]: https://github.com/golusoris/golusoris/actions/workflows/praetor-docs.yml
<!-- praetor:readme-governance:end -->
