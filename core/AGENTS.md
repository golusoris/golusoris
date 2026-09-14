<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/ (module `github.com/golusoris/golusoris/core`)

The **lean sub-module** every consumer can afford (ADR-0017): configuration,
logging, clock, errors, crypto, ids, validation, build info, CLI kit, MCP
server, YAML codec, execution receipts, git runner, Go-source analysis, and
the capability-contract schema. ~20 direct dependencies; no database, HTTP,
cloud, or CGO code lives here — those stay in the root module and import core.

## Packages

| Package | Capability keys | Purpose |
|---|---|---|
| `config/` | `config.loader`, `config.env`, `config.watch` | koanf v2: env + file + YAML, hot reload |
| `codec/yaml/` | `config.yaml` | strict, bounded YAML codec; atomic `WriteFile` |
| `log/` | `telemetry.logging` | slog factory (tint / JSON) |
| `clock/` | `time.clock` | injectable clock — `time.Now` is banned elsewhere |
| `errors/` | `errors.typed`, `errors.problem_details` | typed errors → RFC 9457 status mapping |
| `crypto/` | `crypto.password`, `crypto.aead`, `crypto.token` | argon2id, AES-GCM, secure tokens |
| `crypto/receipt/` | `crypto.receipt` | Ed25519 Exit-0 receipts |
| `id/` | `id.uuid`, `id.ksuid` | UUIDv7 / KSUID |
| `validate/` | `validate.struct` | go-playground/validator wrapper |
| `version/` | `build.version` | build metadata from ldflags / VCS |
| `clikit/` | `clikit.cli`, `clikit.cobra`, `clikit.ioc` | cobra + fx CLI builder (`tui/` stays in the root module) |
| `mcp/` | `mcp.server` | MCP server fx module (stdio / streamable-HTTP) |
| `gitx/`, `gitx/worktree/` | `git.runner`, `git.worktree` | bounded git exec; per-task worktrees |
| `astx/` | `ast.analyzer`, `ast.rewrite` | source walker, import rewriter, func metrics, go.mod reader |
| `capabilities/` | `needs.capabilities` | schema + loader for `capabilities.yaml` |

## Rules specific to core

- **Dependency budget.** Adding a third-party module to `core/go.mod` is a
  reviewed decision; the point of the module is a small trust surface. Heavy
  or CGO code belongs in the root module or its own sub-module.
- **No imports from the root module.** Core must never import
  `github.com/golusoris/golusoris/<root-pkg>` — that is a cycle. Tests use
  `go.uber.org/fx/fxtest` directly, not `testutil/`.
- **Every package is listed in `capabilities.yaml`** with
  `module: github.com/golusoris/golusoris/core`; the root drift test enforces it.
- **Release together.** Tag `core/vX.Y.Z` and `vX.Y.Z` on the same commit; the
  root `go.mod` requires the matching `core` version (in-repo `replace` for dev).

## Developing

```sh
cd core && go build ./... && go test -race ./... && golangci-lint run --config ../.golangci.yml ./...
# or from the repo root:
make build-all ci-all
```
