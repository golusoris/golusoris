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

- **[Principles](principles.md)** — the framework's coding & compliance contract (Power-of-10, SEI CERT, Google Go Style, RFC 9457, SLSA L3).
- **[Architecture Decisions](adr/README.md)** — Nygard-format ADRs, one per decision.
- **[Architecture](architecture/README.md)** — C4 diagrams for the system.
- **[Migration Guides](migrations/v0.1.x.md)** — per-release upgrade notes.
- **[Upstream Snapshots](upstream/README.md)** — version-pinned API references for the framework's dependencies.

The source lives on [GitHub](https://github.com/golusoris/golusoris).
