# Agent guide — golusoris

> Cross-tool context for [Claude Code](https://claude.com/claude-code), [Cursor](https://cursor.sh), [Aider](https://aider.chat), [Codex](https://github.com/openai/codex), [Continue](https://continue.dev), and other coding assistants.
> **Read this before suggesting changes.** Then read the per-subpackage `AGENTS.md` for the area you're touching.

## What this repo is

`golusoris` is a single Go module (`github.com/golusoris/golusoris`) that wraps a pinned set of best-in-class libraries behind opt-in `go.uber.org/fx` modules. Apps compose only what they need — nothing else ships. See [README.md](README.md) for the full module catalog and [docs/principles.md](docs/principles.md) for the complete coding contract.

## Hard rules

1. **Never break public API without a `Migration:` footer.** CI runs `apidiff` against the previous tag.
2. **Never add a transitive dependency** without weighing awesome-go alternatives. State the choice in the PR if non-obvious.
3. **Every subpackage exposes its capability as `fx.Module` or `fx.Options`.** Apps never import internals directly.
4. **No `init()` side effects.** All wiring goes through fx lifecycle hooks.
5. **All errors flow through `golusoris/errors`** (or `fmt.Errorf("pkg: op: %w", err)` — same convention).
6. **All time uses `golusoris/clock`.** `time.Now()` is banned outside the clock package.
7. **Logs go through the slog handler from `golusoris/log`.** No `fmt.Println`, no global loggers.
8. **Every merged commit: 0 lint · 0 gosec · 0 govulncheck · race-green.** `//nolint` requires a justification comment.

See [docs/principles.md](docs/principles.md) for the full Power-of-10, CERT, style, and compliance contract.

## Repository layout

```
golusoris/
├── golusoris.go              # top-level fx.Module re-exports (Core, DB, HTTP, …)
│
├── config/                   # koanf v2: env + file + YAML + file-watch
├── log/                      # slog factory: tint(dev)/JSON(prod) + OTel bridge
├── errors/                   # typed errors + stack traces + ogen-status mapping
├── crypto/                   # argon2id · AES-GCM · sealed secrets · column encryption
├── clock/                    # mockable Clock (real + fake) — time.Now() ban
├── id/                       # UUIDv7 · KSUID · snowflake generators
├── validate/                 # go-playground/validator wrapper
├── i18n/                     # locale negotiation middleware + message catalog
│
├── db/
│   ├── pgx/                  # pgx pool fx module + startup retry + slow-query logger
│   ├── migrate/              # golang-migrate v4 runner + fx hook
│   ├── sqlc/                 # shared sqlc.yaml fragment + query helpers
│   ├── geo/                  # PostGIS pgx types — Point, BBox, EWKB, Haversine
│   ├── timescale/            # TimescaleDB hypertable + retention + compression
│   ├── clickhouse/           # ClickHouse OLAP fx module
│   └── cdc/                  # pglogrepl WAL consumer → typed Event
│
├── httpx/
│   ├── server/               # *http.Server + slow-loris + body limits + graceful stop
│   ├── router/               # chi router → chi.Router + http.Handler in fx
│   ├── middleware/           # logger · recovery · requestid · OTel · secure-headers
│   ├── client/               # retry + circuit-breaker + OTel HTTP client
│   ├── cors/                 # rs/cors middleware
│   ├── csrf/                 # gorilla/csrf middleware
│   ├── ratelimit/            # per-IP/per-user rate limiting (ulule/limiter)
│   ├── ws/                   # WebSocket fan-out helpers
│   ├── form/                 # HTML form → struct decoder
│   ├── static/               # static files + ETag + cache headers
│   │   └── hashfs/           # hashed-asset embed FS + gzip/brotli
│   ├── vite/                 # Vite manifest reader
│   ├── htmx/                 # HX-* response header helpers
│   ├── autotls/              # autocert + certmagic (pluggable TLS)
│   ├── geofence/             # IP/country allow-deny middleware
│   └── rangeserve/           # HTTP range-request serving
│
├── ogenkit/                  # ogen server adapter + RFC 9457 error mapper
├── apidocs/                  # Scalar UI (/docs) + MCP-from-OpenAPI (/mcp)
│
├── otel/                     # full OTel SDK (tracer + meter + logs + OTLP)
├── observability/
│   ├── sentry/               # Sentry fx module (slog + OTel bridged)
│   ├── profiling/            # Pyroscope in-process + eBPF mode
│   ├── pprof/                # auth-gated /debug/pprof endpoint
│   └── statuspage/           # /status page — uptime + dependency health
│
├── k8s/
│   ├── podinfo/              # downward-API env → PodInfo
│   ├── health/               # /livez /readyz /startupz
│   ├── metrics/prom/         # Prometheus /metrics + check-status gauges
│   └── client/               # client-go + workload identity (GKE/EKS/Azure)
│
├── container/runtime/        # detect runtime (k8s/docker/podman/systemd/bare)
├── leader/                   # pluggable leader-election
│   ├── k8s/                  # Kubernetes Lease backend
│   └── pg/                   # PostgreSQL advisory-lock backend
├── systemd/                  # sd_notify + watchdog
│
├── jobs/                     # river client + worker registry + periodic helpers
│   ├── cron/                 # cron expression parser/validator
│   ├── ui/                   # river-ui admin dashboard mount
│   └── workflow/             # Temporal orchestration fx module
│
├── outbox/                   # transactional outbox → river dispatcher
│   └── cdc/                  # CDC-based drain → Kafka/NATS/webhook sinks
│
├── cache/
│   ├── memory/               # otter v2 typed in-memory cache
│   ├── redis/                # rueidis fx module + distributed locks
│   └── singleflight/         # typed de-dupe wrapper
│
├── auth/
│   ├── oidc/                 # OIDC + PKCE + session storage
│   ├── passkeys/             # WebAuthn + TOTP (MFA)
│   ├── jwt/                  # JWT issuance + validation + rotation
│   ├── apikey/               # API key issuance + rotation + scopes
│   ├── magiclink/            # passwordless email-link sign-in
│   ├── linking/              # multi-IdP identity linking
│   ├── impersonate/          # audited admin impersonation
│   ├── session/              # server-side session + revoke UI
│   ├── recovery/             # recovery codes + forgot-password flow
│   ├── policy/               # zxcvbn password strength + HIBP breach check
│   ├── lockout/              # per-identity brute-force lockout
│   ├── oauth2server/         # OAuth 2.1 IdP (fosite)
│   ├── scim/                 # SCIM 2.0 user + group provisioning
│   └── captcha/              # Turnstile + hCaptcha + reCAPTCHA verifiers
├── authz/                    # Casbin RBAC/ABAC
│
├── notify/                   # unified Sender + Notifier + SMTP
│   ├── resend/  postmark/  sendgrid/  mailgun/  ses/   # email senders
│   ├── twilio/  fcm/  apns2/  webpush/  telegram/  teams/  discord/  slack/
│   ├── unsub/               # RFC 8058 one-click unsubscribe
│   └── bounce/              # SES/Postmark bounce + complaint handlers
│
├── realtime/
│   ├── sse/                 # Server-Sent Events hub
│   └── pubsub/              # pub/sub — pg LISTEN/NOTIFY + redis + NATS
│
├── webhooks/
│   ├── in/                  # inbound signature verification
│   └── out/                 # outbound delivery + retry + dead-letter
│
├── tenancy/                 # tenant context middleware + extractors
├── idempotency/             # Idempotency-Key middleware
├── flags/                   # OpenFeature flags + postgres provider
├── audit/                   # append-only audit event log
├── page/                    # typed cursor + offset pagination
│
├── storage/                 # Bucket interface + local backend
│   ├── presign/             # S3 direct-browser upload helpers
│   ├── tus/                 # resumable uploads (tus)
│   ├── safety/              # EXIF strip + SSRF + path-traversal guards
│   └── scan/                # ClamAV malware scan
├── archive/                 # zip/tar/rar/7z/brotli/zstd extract + create
├── media/
│   ├── av/                  # FFmpeg probe + transcode (CGO sub-module)
│   ├── img/                 # image resize + format (CGO sub-module)
│   ├── cv/                  # face/object detection + thumbnails (CGO sub-module)
│   ├── audio/               # audio decode/encode/analyse
│   ├── game/                # Ebitengine 2D game scaffold (own go.mod)
│   └── 3d/                  # g3n 3D engine scaffold (own go.mod)
├── ocr/                     # OCR text extraction (CGO sub-module, own go.mod)
├── pdf/                     # HTML → PDF (chromedp)
│   └── parse/               # PDF metadata + validate + merge (pdfcpu)
├── docs/
│   ├── xlsx/                # XLSX read + write (excelize)
│   ├── docx/                # DOCX template substitution
│   └── epub/                # EPUB 3.0 generator
├── markdown/                # Markdown → HTML (goldmark GFM)
├── htmltmpl/                # type-safe HTML templates (templ)
├── jsonschema/              # JSON Schema 2020-12 validation
├── hash/                    # SHA-256, BLAKE3, xxhash-64, ETag helpers
├── fs/watch/                # recursive dir watch with debounce
├── httpx/rangeserve/        # HTTP range serving for video/large files
│
├── search/                  # Indexer/Searcher interface + MemorySearcher
├── ai/
│   ├── llm/                 # unified Chat/Stream/Embed (Anthropic + OpenAI + Ollama)
│   └── vector/              # pgvector schema + similarity + hybrid search
│
├── payments/
│   ├── stripe/              # Stripe Checkout + Portal + Payment Intents
│   ├── subs/                # subscription state machine
│   ├── meter/               # usage metering
│   └── invoice/             # PDF invoicing
├── money/                   # currency-aware Money type (ISO 4217)
│
├── geoip/                   # MaxMind GeoLite2 country/city/ASN lookups
├── secrets/                 # Secret interface + env/file/static backends
├── integrations/goenvoy/    # fx adapter for github.com/golusoris/goenvoy
│
├── grpc/                    # gRPC server + ConnFactory (OTel + slog + recovery)
├── graphql/                 # gqlgen server (APQ, complexity, GraphiQL)
│   └── client/              # genqlient typed GraphQL client
├── pubsub/
│   ├── kafka/               # franz-go Kafka producer + consumer
│   └── nats/                # NATS JetStream
├── net/
│   ├── wol/                 # Wake-on-LAN magic-packet sender (stdlib)
│   ├── dnsserver/           # DNS server — UDP + TCP (miekg/dns)
│   └── smtpserver/          # Inbound SMTP server (emersion/go-smtp)
├── ebpf/                    # cilium/ebpf scaffold (ObjectProvider + Registry)
│
├── science/
│   ├── numerical/           # gonum linear algebra + stats (own go.mod)
│   ├── plot/                # gonum/plot chart rendering (own go.mod)
│   └── bio/                 # bioinformatics — FASTA, rev-comp (own go.mod)
├── web3/
│   ├── evm/                 # Ethereum/EVM client + key gen (own go.mod)
│   └── solana/              # Solana RPC + keypair (own go.mod)
├── hw/
│   ├── gpio/                # GPIO/I²C/SPI via periph.io (own go.mod)
│   ├── robotics/            # gobot scaffold (own go.mod)
│   ├── udev/                # Linux udev events (own go.mod)
│   └── fssnap/              # ZFS + Btrfs snapshot CLI wrappers (own go.mod)
│
├── clikit/                  # cobra + fx-aware CLI app builder
│   └── tui/                 # bubbletea Run/RunInline helpers
├── selfupdate/              # binary self-update from GitHub releases
├── plugin/                  # generic thread-safe Registry[T]
│
├── testutil/
│   ├── pg/                  # testcontainers PostgreSQL
│   ├── redis/               # testcontainers Redis
│   ├── river/               # in-process river test harness
│   ├── fxtest/              # fx lifecycle helpers for tests
│   ├── snapshot/            # golden-file / snapshot testing
│   ├── factory/             # deterministic gofakeit test data factories
│   ├── fuzz/                # fuzz corpus directory helpers
│   ├── load/                # vegeta load-test harness
│   ├── mutation/            # go-mutesting runner + score assertion
│   ├── prop/                # gopter property-based testing (own go.mod)
│   └── pact/                # Pact contract testing (own go.mod)
│
├── cmd/
│   ├── golusoris/           # scaffolder CLI (init / add / bump + codemods)
│   └── golusoris-mcp/       # MCP JSON-RPC server
│
├── deploy/
│   ├── helm/                # base Helm chart
│   ├── observability/       # PrometheusRule + Grafana dashboard
│   ├── logging/             # Loki + Promtail config
│   ├── terraform/           # Terraform modules (VPC/RDS/ElastiCache/S3/IAM)
│   ├── flux/                # Flux GitOps manifests
│   ├── argocd/              # Argo CD Application manifests
│   └── crossplane/          # XRD + Composition + claim example
│
├── tools/                   # golangci.yml, sqlc.yaml.fragment, Makefile helpers
├── template/
│   ├── .github/             # per-app CI + release workflow stubs, dependabot
│   └── .devcontainer/       # Go + Postgres + Redis + NATS devcontainer
│
├── docs/
│   ├── principles.md        # full coding + security + compliance contract (§2)
│   ├── adr/                 # Architecture Decision Records (Nygard format)
│   ├── architecture/        # C4 PlantUML diagrams (Context + Container)
│   ├── migrations/          # per-version API migration guides
│   └── upstream/            # pinned upstream docs snapshots
│
├── examples/
│   ├── minimal/             # Core + DB + OTel + HTTP + K8s health
│   └── full/                # all major modules composed
│
└── AGENTS.md  CLAUDE.md     # this file + Claude-specific instructions
```

Per-subpackage `AGENTS.md` files give package-level conventions, idioms, and pinned doc URLs.

## Common tasks

| Task | Command / Skill |
|---|---|
| Add a new fx module | `/wire-fx-module` skill — see `.claude/skills/` |
| Add an ogen handler stub | `/scaffold-ogen-handler` skill |
| Add a river background worker | `/add-river-worker` skill |
| Add a DB migration | `/add-migration` skill |
| Bump golusoris in a downstream app | `/bump-golusoris` skill or `golusoris bump <version>` |

## Pinned upstream docs

Version-pinned snapshots live in `docs/upstream/`. Consult these before suggesting API patterns — public docs may be ahead or behind the pinned version.

| Package | Pinned version |
|---|---|
| `go.uber.org/fx` | v1.24.0 |
| `jackc/pgx/v5` | v5.9.1 |
| `ogen-go/ogen` | v1.20.3 |
| `riverqueue/river` | v0.34.0 |
| `knadh/koanf/v2` | v2.3.4 |

## CI gates

Every PR must pass:

- `golangci-lint` (30+ linters — see `tools/golangci.yml`)
- `govulncheck`
- `go test -race -count=1` + 70% coverage (85% on security-critical packages)
- `apidiff` vs previous tag — no undeclared breaking changes
- Conventional-commit PR title

## When in doubt

Read [docs/principles.md](docs/principles.md) for the full coding contract, then read the per-subpackage `AGENTS.md` for the area you're touching.
