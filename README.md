# golusoris

[![Go Reference](https://pkg.go.dev/badge/github.com/golusoris/golusoris.svg)](https://pkg.go.dev/github.com/golusoris/golusoris)
[![Go Report Card](https://goreportcard.com/badge/github.com/golusoris/golusoris)](https://goreportcard.com/report/github.com/golusoris/golusoris)
[![Go Version](https://img.shields.io/github/go-mod/go-version/golusoris/golusoris)](go.mod)
[![CI](https://github.com/golusoris/golusoris/actions/workflows/ci.yml/badge.svg)](https://github.com/golusoris/golusoris/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/golusoris/golusoris/badge)](https://scorecard.dev/viewer/?uri=github.com/golusoris/golusoris)
[![ko-fi](https://img.shields.io/badge/ko--fi-support-FF5E5B?logo=ko-fi&logoColor=white)](https://ko-fi.com/lusoris)

A composable Go framework built around `go.uber.org/fx`. Provides opt-in modules for everything a production backend needs — config, logging, errors, DB, HTTP, OTel, jobs, cache, auth, k8s runtime, notifications, files, AI, and more — so apps share one source of truth for cross-cutting concerns.

```go
import "github.com/golusoris/golusoris"

fx.New(
    golusoris.Core,            // config + log + lifecycle + errors + clock + id
    golusoris.DB,              // pgx pool + migrations + sqlc helpers
    golusoris.OTel,            // tracer + meter + logs + OTLP
    golusoris.HTTP,            // server + standard middleware + Scalar docs
    golusoris.Auth.OIDC,
    golusoris.Jobs,            // river queue
    golusoris.Cache.Memory,
    golusoris.K8s.Health,      // /livez /readyz /startupz
    // ... pick what you need
).Run()
```

## Status

Pre-alpha. See [.workingdir/PLAN.md](.workingdir/PLAN.md) + [.workingdir/STATE.md](.workingdir/STATE.md) for scope + progress.

### Landed so far

- **Step 1 — Core**: `config/` `log/` `errors/` `clock/` `id/` `validate/` `i18n/` `crypto/`
- **Step 2 — DB**: `db/pgx/` `db/migrate/` `db/sqlc/` `testutil/pg/`
- **Step 3 — HTTP base**: `httpx/server/` `httpx/router/` `httpx/middleware/` `httpx/client/` `ogenkit/` `apidocs/` (Scalar + MCP-from-OpenAPI)
- **Step 4 — HTTP extras**: `httpx/form/` `httpx/htmx/` `httpx/vite/` `httpx/static/` `httpx/static/hashfs/` `httpx/cors/` `httpx/csrf/` `httpx/ratelimit/` `httpx/geofence/` `httpx/ws/` `httpx/autotls/` (autocert + certmagic)
- **Step 5 — OTel + observability**: `otel/` (tracer + meter + logs + OTLP) `observability/sentry/` `observability/profiling/` (Pyroscope) `observability/pprof/` `observability/statuspage/`
- **Step 6 — K8s runtime**: `k8s/podinfo/` `k8s/health/` (`/livez` `/readyz` `/startupz`) `k8s/metrics/prom/` (`/metrics` + check-status gauges) `k8s/client/` (in-cluster + kubeconfig + transparent GKE/EKS/Azure workload identity)
- **Step 6.5 — Runtime-agnostic + Docker/systemd**: `container/runtime/` (unified k8s/docker/podman/systemd/bare detection) · `leader/` (pluggable: `leader/k8s` Lease, `leader/pg` advisory lock) · `systemd/` (sd_notify + watchdog) · Docker Compose + Prometheus scrape-config examples in `tools/`
- **Step 7 — Jobs + outbox**: `jobs/` (river client + workers registry) · `jobs/cron/` (periodic helpers) · `jobs/ui/` (admin dashboard) · `outbox/` (transactional outbox → river dispatcher, leader-gated) · `testutil/river/` (test harness with real Postgres + river migrations)
- **Step 8 — Cache**: `cache/memory/` (otter v2, typed L1) · `cache/redis/` (rueidis, standalone + cluster) · `cache/singleflight/` (typed dedup wrapper) · `testutil/redis/` (real Redis container)
- **Step 9 — Auth + Authz** (partial): `auth/jwt/` · `auth/apikey/` (HMAC-SHA256, pluggable store) · `auth/oidc/` (PKCE, go-oidc/v3) · `auth/session/` (server-side, pluggable store) · `authz/` (Casbin RBAC/ABAC)
- **Step 10 — Notify + Realtime** (partial): `notify/` (Sender interface + SMTP via go-mail) · `notify/unsub/` (RFC 8058 one-click) · `realtime/sse/` (SSE hub) · `realtime/pubsub/` (in-process + Bus interface)

## Modules (high level)

Core • DB • HTTP/API (ogen + Scalar + MCP) • Auth (OIDC + Passkeys + Casbin) • Jobs (river) • Cache (otter + rueidis) • OTel/Sentry/Pyroscope • K8s runtime (probes, leader, podinfo) • Notifications • Realtime (SSE/PubSub) • Webhooks • SaaS primitives (tenancy, idempotency, audit, flags) • Storage/Media (S3, FFmpeg, libvips, OCR, archive) • Search & AI (typesense, pgvector, LLM) • Commerce (Stripe) • Integrations (goenvoy adapter, geoip, secrets) • CLI/MCP scaffolders.

## Tooling

`make ci` runs golangci-lint + govulncheck + gosec + tests.

## License

[MIT](LICENSE).

## Support

If golusoris saves you time, a coffee goes a long way ☕

<p align="left">
  <a href="https://ko-fi.com/lusoris" target="_blank">
    <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="Support me on Ko-fi" />
  </a>
  &nbsp;&nbsp;
  <a href="https://github.com/sponsors/lusoris" target="_blank">
    <img src="https://img.shields.io/badge/Sponsor-%E2%9D%A4-ea4aaa?style=for-the-badge&logo=github&logoColor=white" alt="GitHub Sponsors" />
  </a>
</p>
