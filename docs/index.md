<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# golusoris

A composable Go framework built around [`go.uber.org/fx`](https://github.com/uber-go/fx). Pick the modules your app needs — nothing else ships.

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

## Documentation map

- **[Contributing](https://github.com/golusoris/golusoris/blob/main/CONTRIBUTING.md)** — Conventional Commits, DCO sign-off, CI gates, local dev commands, git hooks, and the release process.
- **[Principles](principles.md)** — the framework's coding & compliance contract (Power-of-10, SEI CERT, Google Go Style, RFC 9457, SLSA L3).
- **[Architecture Decisions](adr/README.md)** — Nygard-format ADRs, one per decision.
- **[Architecture](architecture/README.md)** — C4 diagrams for the system.
- **[Migration Guides](migrations/v0.9.0.md)** — per-release upgrade notes (v0.9.0: `core/` sub-module import paths).
- **[Capability contract](https://github.com/golusoris/golusoris/blob/main/capabilities.yaml)** — every package → capability keys → replaced modules; what praetor `needs` resolves against.
- **[Licensing](https://github.com/golusoris/golusoris/blob/main/LICENSING.md)** — EUPL-1.2 code, CC-BY-SA-4.0 prose, REUSE, DCO.
- **[Governance](https://github.com/golusoris/golusoris/blob/main/GOVERNANCE.md)** — project scope, decision process, and maintainer roles.
- **[Security](https://github.com/golusoris/golusoris/blob/main/SECURITY.md)** — supported versions and how to report a vulnerability.
- **[Upstream Snapshots](upstream/README.md)** — version-pinned API references for the framework's dependencies.
- **[CI / Downstream Consumption](ci-downstream.md)** — how apps consume `tools/Makefile.shared` and the reusable GitHub Actions workflows.

The source lives on [GitHub](https://github.com/golusoris/golusoris).
