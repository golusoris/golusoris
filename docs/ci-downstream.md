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
| `make lint` | `golangci-lint run --config $(GOLANGCI_CONFIG)` (default `tools/golangci.yml`) |
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
GOLANGCI_CONFIG := tools/golangci.yml   # point at your own or the shared config
include tools/Makefile.shared
```

Scope a run to a subtree with `PKG`:

```sh
make test PKG=./internal/payments/...
```

## 2. Reusable CI workflow — `ci-go.yml`

The framework publishes reusable **GitHub Actions** workflows under
`.github/workflows/`. Call `ci-go.yml` via `workflow_call` from your app. It
runs, as separate jobs: Conventional-Commits PR-title check, lint
(golangci-lint), security (gosec, SARIF upload), vulnerabilities (govulncheck),
test (`-race` on Linux + macOS with a coverage-threshold gate), build, OpenAPI
spectral lint, and apidiff.

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
      go-version-file: go.mod
      coverage-threshold: 70          # 85 for security-critical packages
      module-path: github.com/myorg/myapp
    secrets: inherit
```

Coverage is **not** a separate workflow — it's the `coverage-threshold` input
on the test job (set `0` to skip the gate).

Common inputs (all optional except where noted; see the `workflow_call` block
at the top of `.github/workflows/ci-go.yml` for the full list and defaults):

| Input | Default | Purpose |
|---|---|---|
| `go-version-file` | `go.mod` | where the Go version is resolved from |
| `coverage-threshold` | `70` | minimum total coverage %; `0` skips the check |
| `golangci-version` | `v2.12.2` | golangci-lint version to install |
| `golangci-config` | *(empty)* | path to a shared ruleset; empty = auto-discover the app's `.golangci.yml` |
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
| `gosec` | `v2.25.0` | `go install github.com/securego/gosec/v2/cmd/gosec@v2.25.0` |
| `govulncheck` | latest | `go install golang.org/x/vuln/cmd/govulncheck@latest` |
| `mockery` | — | `go install github.com/vektra/mockery/v2@latest` |
| `air` | — | `go install github.com/air-verse/air@latest` |

## 5. golangci-lint config

`tools/golangci.yml` is a golangci-lint **v2** config and the shared baseline
for the framework and downstream apps. Point `GOLANGCI_CONFIG` (local) and the
`golangci-config` input (CI) at it, or extend it in your app:

```yaml
# myapp/.golangci.yml  (golangci-lint v2)
version: "2"
# copy the shared tools/golangci.yml and layer app-specific overrides here,
# or vendor it and set golangci-config: tools/golangci.yml in ci-go.yml.
```

If `golangci-config` is left empty in `ci-go.yml`, golangci-lint auto-discovers
the app's own `.golangci.yml`; set it to opt into the shared ruleset explicitly.

## 6. Pre-commit hook

`make ci` is the intended pre-commit check. Wire it with a plain git hook:

```sh
echo 'make ci' > .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit
```

or with a hook manager such as [lefthook](https://github.com/evilmartians/lefthook):

```yaml
# lefthook.yml
pre-commit:
  commands:
    ci:
      run: make ci
```
