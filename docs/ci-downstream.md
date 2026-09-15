<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Consuming the framework's shared CI tooling

Downstream apps get lint, security scans, tests, and coverage two ways, which
compose: **`tools/Makefile.shared`** for the local gate, and the framework's
**reusable GitHub Actions workflows** (`ci-go.yml`, `release-go.yml`) for CI.

## 1. Local gate — `tools/Makefile.shared`

Copy or symlink `tools/Makefile.shared` from the framework into your app, then
include it from your root `Makefile`.

Vendor it (pin to the framework version you depend on):

```sh
V=$(go list -m -f '{{.Version}}' github.com/golusoris/golusoris)
cp "$(go env GOPATH)/pkg/mod/github.com/golusoris/golusoris@${V}/tools/Makefile.shared" tools/Makefile.shared
```

```makefile
# myapp/Makefile
include tools/Makefile.shared
```

Or include it straight from the module cache, no copy:

```makefile
# myapp/Makefile
GOLUSORIS ?= $(shell go env GOPATH)/pkg/mod/github.com/golusoris/golusoris@$(shell go list -m -f '{{.Version}}' github.com/golusoris/golusoris)
include $(GOLUSORIS)/tools/Makefile.shared
```

Targets after inclusion:

| Target | What it runs |
|---|---|
| `make ci` | `lint` + `sec` + `test` — the full local gate |
| `make lint` | `golangci-lint run --config $(GOLANGCI_CONFIG)` (default `.golangci.yml`) |
| `make sec` | `vuln` + `gosec` (`govulncheck` then `gosec -quiet`) |
| `make test` | `go test -race -count=1 -timeout=120s ./...` |
| `make cover` | `go test` with `-coverprofile=coverage.out`, renders `coverage.html` |
| `make build` | `go build -trimpath ./cmd/...` |
| `make tidy` | `go mod tidy` |
| `make gen` | `sqlc` + `ogen` + `mockery` |
| `make dev` | `air` hot-reload |
| `make spec-lint` | `spectral lint` the OpenAPI spec |

Override any tool binary or path by setting its variable before inclusion —
every tool is a `?=` default:

```makefile
GOLANGCI        := $(shell which golangci-lint)
GOLANGCI_CONFIG := .golangci.yml   # point at your own or the shared config
include tools/Makefile.shared
```

Scope a run to a subtree with `PKG`:

```sh
make test PKG=./internal/payments/...
```

### Multi-module repositories

The framework itself is two gated Go modules (root + `core/`). `make ci-all`, `make build-all`, and `make verify-all` in the framework `Makefile` loop over both; downstream apps that adopt the same shape can copy the `MODULES` loop.

## 2. Reusable CI workflow — `ci-go.yml`

The framework publishes reusable **GitHub Actions** workflows under
`.github/workflows/`. Call `ci-go.yml` via `workflow_call` from your app. It
runs, as separate jobs: Conventional-Commits PR-title check, lint
(golangci-lint), security (gosec, SARIF upload), vulnerabilities (govulncheck),
test (`-race` with a coverage-threshold gate — Linux by default via the
`runs-on` input; the job's steps also branch for a macOS leg, skipping
Docker-requiring packages, for a caller that wraps this workflow in its own
`runs-on` matrix), build, OpenAPI spectral lint, and apidiff.

```yaml
# myapp/.github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  ci:
    uses: golusoris/golusoris/.github/workflows/ci-go.yml@main
    with:
      runs-on: arc-cauda-golusoris-myapp   # your own ARC runner-set label
      working-directory: .                  # dir holding go.mod; "." for a root module
      go-version-file: go.mod
      coverage-threshold: 70          # 85 for security-critical packages
      module-path: github.com/myorg/myapp
    secrets: inherit
```

Pin `@main` to a specific commit before relying on this in production — see
"Pinning the reusable-workflow reference" below.

Coverage is **not** a separate workflow — it's the `coverage-threshold` input
on the test job (set `0` to skip the gate).

### Calling `ci-go.yml` for more than one module

If your app is also a multi-module repo, invoke `ci-go.yml` once per module
(each `uses:` gets its own `working-directory`). The workflow is matrix-safe
for this since [#498](https://github.com/golusoris/golusoris/pull/498): the
coverage artifact is named `coverage-<working-directory-slug>` (or plain
`coverage` at the repo root) instead of a fixed `coverage`, and the gosec
SARIF upload is tagged with a `gosec[/<working-directory>]` code-scanning
category — both derived from `working-directory`, so two calls in the same
run no longer collide on the artifact name or overwrite each other's SARIF
upload.

### Pinning the reusable-workflow reference

The example above uses `@main` for readability. The app template this
framework ships under [`template/`](../template/) pins the same calls to a
full commit SHA with a version comment instead —
[`template/.github/workflows/ci.yml`](https://github.com/golusoris/golusoris/blob/main/template/.github/workflows/ci.yml)
and [`release.yml`](https://github.com/golusoris/golusoris/blob/main/template/.github/workflows/release.yml) both reference
`golusoris/golusoris/.github/workflows/<workflow>.yml@380b26797a8552c8b8aba03d53209b8997f2b1be # v0.10.1`.
Do the same in a real app: pin to a released tag's commit SHA (and bump it
deliberately) rather than floating on `@main`, so an unreviewed change to
this repository's default branch cannot silently change your CI.

Common inputs (all optional except where noted; see the `workflow_call` block
at the top of `.github/workflows/ci-go.yml` for the full list and defaults):

| Input | Default | Purpose |
|---|---|---|
| `runs-on` | `arc-cauda-golusoris-golusoris` | self-hosted runner-set label every job runs on; set to your own app's ARC label |
| `working-directory` | `.` | directory holding your module's go.mod, relative to the repo root; set for apps whose module is not at the repo root |
| `go-version-file` | `go.mod` | where the Go version is resolved from — **relative to the repo root**, not `working-directory` (see the input's own description in `ci-go.yml`); a non-root module must pass e.g. `<working-directory>/go.mod` |
| `coverage-threshold` | `70` | minimum total coverage %; `0` skips the check |
| `golangci-version` | `v2.12.2` | golangci-lint version to install |
| `golangci-config` | *(empty)* | path to a shared ruleset, relative to `working-directory`; empty = auto-discover the app's `.golangci.yml` in that same directory |
| `module-path` | *(empty)* | module path for the apidiff check, e.g. `github.com/myorg/myapp` |
| `needs-docker` | `true` | verify Docker before tests; set `false` if no testcontainers |
| `container` | *(empty)* | image to run the Go jobs in (cgo/system-lib builds) |
| `system-packages` | *(empty)* | Debian packages that the rootless ARC image must already contain |
| `openapi-spec` | *(empty)* | path to an OpenAPI spec for spectral lint; empty = skip |
| `skip-apidiff` | `false` | set `true` for a first release with no prior tag |

## 3. Reusable release workflow — `release-go.yml`

Runs on `v*.*.*` tags and produces a multi-arch OCI image (GHCR), an SPDX SBOM
via syft, a keyless cosign signature, and SLSA build provenance.

```yaml
# myapp/.github/workflows/release.yml
name: Release

on:
  push:
    tags: ["v*.*.*"]

permissions:
  contents: write     # attach release assets
  packages: write     # push image to GHCR
  id-token: write     # keyless cosign + provenance attestation

jobs:
  release:
    uses: golusoris/golusoris/.github/workflows/release-go.yml@main
    with:
      runs-on: arc-cauda-golusoris-myapp   # your own ARC runner-set label
      image-name: ghcr.io/myorg/myapp     # required
      goreleaser-config: tools/.goreleaser.yml
    secrets: inherit                       # COSIGN_PASSWORD is optional
```

## 4. Required tools + versions

`make ci` expects these on `PATH` locally. In CI, `ci-go.yml` installs its own
pinned copies (the versions below match what the reusable workflow pins as of
this writing — check the workflow for the current pins):

| Tool | Version pinned in `ci-go.yml` | Install locally |
|---|---|---|
| `golangci-lint` | `v2.12.2` | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2` |
| `gosec` | `v2.27.1` | `go install github.com/securego/gosec/v2/cmd/gosec@v2.27.1` |
| `govulncheck` | `v1.4.0` | `go install golang.org/x/vuln/cmd/govulncheck@v1.4.0` |
| `mockery` | — | `go install github.com/vektra/mockery/v2@latest` |
| `air` | — | `go install github.com/air-verse/air@latest` |

## 5. golangci-lint config

`.golangci.yml` (repo root — golangci-lint's own auto-discovery path, and
where praetor's flavor audit expects it) is a golangci-lint **v2** config and
the shared baseline for the framework and downstream apps. Point
`GOLANGCI_CONFIG` (local) and the `golangci-config` input (CI) at your copy,
or extend it in your app:

```yaml
# myapp/.golangci.yml  (golangci-lint v2)
version: "2"
# copy the shared .golangci.yml and layer app-specific overrides here,
# or vendor it under a different name and set golangci-config accordingly
# in ci-go.yml.
```

If `golangci-config` is left empty in `ci-go.yml`, golangci-lint auto-discovers
the app's own `.golangci.yml` at its repo root — the recommended default, and
the same file praetor's flavor audit looks for; set `golangci-config` only to
opt into a path other than your own root `.golangci.yml`.

## 6. Git hooks

`make ci` is the intended local gate for apps. The framework itself uses
[lefthook](https://github.com/evilmartians/lefthook) with finer-grained hooks
(below); the minimal app-side configuration is:

```yaml
# lefthook.yml
pre-commit:
  commands:
    ci:
      run: make ci
```

The framework's own [`lefthook.yml`](https://github.com/golusoris/golusoris/blob/main/lefthook.yml) + `scripts/hooks/` is a
finer-grained template (staged-package lint, commit-msg Conventional Commits +
DCO, pre-push build + `go test -short`) that apps can copy verbatim.
