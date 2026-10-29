<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# docs/upstream — pinned upstream documentation snapshots

Version-pinned API reference snapshots for AI coding assistants: Claude Code,
Cursor, Aider, Codex, and Continue.

**Why this exists:** Public docs may be ahead of or behind the version the
framework pins. These snapshots keep generated suggestions on the selected API.

**How to update:** The merge train runs `scripts/ci/regenerate.py write` to
regenerate module pin strings in the table and matching snapshot headings,
`Pinned` lines, and `Source` tags. Review the exact selected upstream tag and
edit curated “Changes since” prose by hand; regeneration leaves it untouched.
Run `make docs-upstream` for the recipe, then `make docs-upstream-verify` after
regeneration. The table is the verifier's mapping authority; its authority column
says where each pin is resolved. Scalar pins use `scalar-bundle-update.sh`;
repository tool and Go toolchain rows are reported as skipped by the module pin
regenerator.

## Index

| Package | Pinned version | Authority | Snapshot |
| --- | --- | --- | --- |
| `go.uber.org/fx` | `v1.24.0` | root + core | [fx/](fx/README.md) |
| `github.com/jackc/pgx/v5` | `v5.11.0` | root | [pgx/](pgx/README.md) |
| `github.com/ogen-go/ogen` | `v1.24.0` | root | [ogen/](ogen/README.md) |
| `github.com/riverqueue/river` | `v0.49.0` | root | [river/](river/README.md) |
| `github.com/knadh/koanf/v2` | `v2.3.8` | root + core | [koanf/](koanf/README.md) |
| `github.com/maypok86/otter/v2` | `v2.3.0` | root | [otter/](otter/README.md) |
| `github.com/redis/rueidis` | `v1.0.79` | root | [rueidis/](rueidis/README.md) |
| `github.com/casbin/casbin/v3` | `v3.11.0` | root | [casbin/](casbin/README.md) |
| `github.com/go-webauthn/webauthn` | `v0.18.2` | root | [webauthn/](webauthn/README.md) |
| `go.opentelemetry.io/otel` | `v1.47.0` | root | [otel/](otel/README.md) |
| `github.com/golang-migrate/migrate/v4` | `v4.20.1` | root | [golang-migrate/](golang-migrate/README.md) |
| `github.com/sqlc-dev/sqlc` | `v1.31.1` | repository tool pin | [sqlc/](sqlc/README.md) |
| `@scalar/api-reference` | `v1.75.1` | embedded asset | [scalar/](scalar/README.md) |
| `k8s.io/client-go` | `v0.37.1` | root | [k8s/](k8s/README.md) |
| `github.com/go-chi/chi/v5` | `v5.5.0` | root | [chi/](chi/README.md) |
| `github.com/go-playground/validator/v10` | `v10.30.5` | root + core | [validator/](validator/README.md) |
| `github.com/jonboulle/clockwork` | `v0.5.0` | root + core | [clockwork/](clockwork/README.md) |
| `github.com/yuin/goldmark/v2` | `v2.1.6` | root | [goldmark/](goldmark/README.md) |
| `github.com/prometheus/client_golang` | `v1.25.0` | root | [prometheus/](prometheus/README.md) |
| `github.com/testcontainers/testcontainers-go` | `v0.44.0` | root | [testcontainers/](testcontainers/README.md) |
| `log/slog` | `go1.27.3` | root + core + toolchain | [slog/](slog/README.md) |

Removed snapshots: `a-h/templ` never became a dependency; `htmltmpl/` uses
standard-library `html/template` under ADR-0014.
