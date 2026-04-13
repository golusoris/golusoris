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

Pre-alpha. See [.workingdir/PLAN.md](.workingdir/PLAN.md) (when published) for the full scope and roadmap.

## Modules (high level)

Core • DB • HTTP/API (ogen + Scalar) • Auth (OIDC + Passkeys + Casbin) • Jobs (river) • Cache (otter + rueidis) • OTel/Sentry/Pyroscope • K8s runtime (probes, leader, podinfo) • Notifications • Realtime (SSE/PubSub) • Webhooks • SaaS primitives (tenancy, idempotency, audit, flags) • Storage/Media (S3, FFmpeg, libvips, OCR, archive) • Search & AI (typesense, pgvector, LLM) • Commerce (Stripe) • Integrations (goenvoy adapter, geoip, secrets) • CLI/MCP scaffolders.

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
