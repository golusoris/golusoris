# docs/upstream — pinned upstream documentation snapshots

Version-pinned API reference snapshots for AI coding assistants (Claude Code, Cursor, Aider, Codex, Continue).

**Why this exists:** Public docs may be ahead of or behind the version the framework pins. Consulting these snapshots instead of live docs prevents AI agents from suggesting API patterns that don't exist at the pinned version.

**How to update:** When bumping a dependency, run `make docs-upstream` to re-fetch the relevant snapshot, then commit the diff alongside the version bump.

## Index

| Package | Pinned version | Snapshot |
|---|---|---|
| `go.uber.org/fx` | v1.24.0 | [fx/](fx/) |
| `jackc/pgx/v5` | v5.9.1 | [pgx/](pgx/) |
| `ogen-go/ogen` | v1.20.3 | [ogen/](ogen/) |
| `riverqueue/river` | v0.34.0 | [river/](river/) |
| `knadh/koanf/v2` | v2.3.4 | [koanf/](koanf/) |
| `maypok86/otter/v2` | v2.2.0 | [otter/](otter/) |
| `redis/rueidis` | v1.0.54 | [rueidis/](rueidis/) |
| `casbin/casbin/v2` | v2.105.0 | [casbin/](casbin/) |
| `go-webauthn/webauthn` | v0.11.2 | [webauthn/](webauthn/) |
| `go.opentelemetry.io/otel` | v1.35.0 | [otel/](otel/) |
| `golang-migrate/migrate/v4` | v4.18.3 | [golang-migrate/](golang-migrate/) |
| `sqlc-dev/sqlc` | v1.29.0 | [sqlc/](sqlc/) |
| `scalar/scalar` | — (JS, latest) | [scalar/](scalar/) |
| `k8s.io/client-go` | v0.32.3 | [k8s/](k8s/) |
| `go-chi/chi/v5` | v5.2.1 | [chi/](chi/) |
| `go-playground/validator/v10` | v10.26.0 | [validator/](validator/) |
| `jonboulle/clockwork` | v0.5.0 | [clockwork/](clockwork/) |
| `yuin/goldmark` | v1.7.12 | [goldmark/](goldmark/) |
| `a-h/templ` | v0.3.898 | [templ/](templ/) |
| `prometheus/client_golang` | v1.22.0 | [prometheus/](prometheus/) |
| `testcontainers/testcontainers-go` | v0.37.0 | [testcontainers/](testcontainers/) |
| `log/slog` (stdlib) | go1.26.2 | [slog/](slog/) |
