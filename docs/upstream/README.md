<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# docs/upstream — pinned upstream documentation snapshots

Version-pinned API reference snapshots for AI coding assistants (Claude Code, Cursor, Aider, Codex, Continue).

**Why this exists:** Public docs may be ahead of or behind the version the framework pins. Consulting these snapshots instead of live docs prevents AI agents from suggesting API patterns that don't exist at the pinned version.

**How to update:** When bumping a dependency, run `make docs-upstream` — it prints the refresh recipe (resolve the new pin with `go list -m -f '{{.Version}}' <module>` in the module that requires it, re-fetch the upstream README / API docs at that tag into the matching `docs/upstream/<name>/` directory, update the table below) — then commit the diff alongside the version bump. Pins are taken from the root `go.mod` unless marked `core/` (resolved from `core/go.mod`) or `tool` (a build-time binary, not a Go dependency).

## Index

| Package | Pinned version | Snapshot |
|---|---|---|
| `go.uber.org/fx` | v1.24.0 (also `core/`) | [fx/](fx/) |
| `jackc/pgx/v5` | v5.11.0 | [pgx/](pgx/) |
| `ogen-go/ogen` | v1.24.0 | [ogen/](ogen/) |
| `riverqueue/river` | v0.47.0 | [river/](river/) |
| `knadh/koanf/v2` | v2.3.6 (`core/`) | [koanf/](koanf/) |
| `maypok86/otter/v2` | v2.3.0 | [otter/](otter/) |
| `redis/rueidis` | v1.0.77 | [rueidis/](rueidis/) |
| `casbin/casbin/v3` | v3.11.0 | [casbin/](casbin/) |
| `go-webauthn/webauthn` | v0.18.1 | [webauthn/](webauthn/) |
| `go.opentelemetry.io/otel` | v1.46.0 | [otel/](otel/) |
| `golang-migrate/migrate/v4` | v4.20.1 | [golang-migrate/](golang-migrate/) |
| `sqlc-dev/sqlc` | tool — `make gen` uses the installed binary (`tools/sqlc.yaml.fragment` targets sqlc config v2) | [sqlc/](sqlc/) |
| `scalar/scalar` | — (JS bundle embedded in `apidocs/embed/`, refresh via `make scalar-update`) | [scalar/](scalar/) |
| `k8s.io/client-go` | v0.37.0 | [k8s/](k8s/) |
| `go-chi/chi/v5` | v5.3.2 | [chi/](chi/) |
| `go-playground/validator/v10` | v10.30.4 (`core/`) | [validator/](validator/) |
| `jonboulle/clockwork` | v0.5.0 (`core/`) | [clockwork/](clockwork/) |
| `yuin/goldmark` | v1.8.6 | [goldmark/](goldmark/) |
| `prometheus/client_golang` | v1.24.1 | [prometheus/](prometheus/) |
| `testcontainers/testcontainers-go` | v0.44.0 | [testcontainers/](testcontainers/) |
| `log/slog` (stdlib) | go1.27.0 | [slog/](slog/) |

Removed snapshots: `a-h/templ` (never became a dependency — `htmltmpl/` uses stdlib `html/template`, ADR-0014).
