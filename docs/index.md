<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# golusoris

A composable Go framework built around [`go.uber.org/fx`](https://github.com/uber-go/fx). Pick the modules your app needs — nothing else ships.

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

## Documentation map

- **[Contributing](https://github.com/golusoris/golusoris/blob/main/CONTRIBUTING.md)** — Conventional Commits, DCO sign-off, CI gates, local dev commands, git hooks, and the release process.
- **[Principles](principles.md)** — coding and assurance contract (Power-of-10, SEI CERT, Google Go Style, RFC 9457, and evidence-backed supply-chain controls).
- **[Architecture Decisions](adr/README.md)** — Nygard-format ADRs, one per decision.
- **[Architecture](architecture/README.md)** — C4 diagrams for the system.
- **[Migration Guides](migrations/v0.13.0.md)** — current constructor, atomic-store, secure-default, lifecycle, and resource-bound upgrades; older guides remain in navigation.
- **[Capability contract](https://github.com/golusoris/golusoris/blob/main/capabilities.yaml)** — every package → capability keys → replaced modules; what praetor `needs` resolves against.
- **[Licensing](https://github.com/golusoris/golusoris/blob/main/LICENSING.md)** — EUPL-1.2 code, CC-BY-SA-4.0 prose, REUSE, DCO.
- **[Governance](https://github.com/golusoris/golusoris/blob/main/GOVERNANCE.md)** — project scope, decision process, and maintainer roles.
- **[Security](https://github.com/golusoris/golusoris/blob/main/SECURITY.md)** — supported versions and how to report a vulnerability.
- **[Upstream Snapshots](upstream/README.md)** — version-pinned API references for the framework's dependencies.
- **[CI / Downstream Consumption](ci-downstream.md)** — how apps consume `tools/Makefile.shared` and the reusable GitHub Actions workflows.

The source lives on [GitHub](https://github.com/golusoris/golusoris).
