<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — cmd/golusoris/

Scaffolder CLI built with `clikit/`. Wraps three core subcommands.

## Usage

```sh
golusoris init myapp --module github.com/myorg/myapp
golusoris add db
golusoris add http
golusoris bump v0.5.0
golusoris --version
```

## Subcommands

| Command | Description |
| --- | --- |
| `init <name>` | Scaffold new app directory with `go.mod` + `main.go` |
| `add <module>` | Print how to add module (db, http, otel, cache, jobs, auth-oidc, authz, k8s) |
| `bump <version>` | Run `go get github.com/golusoris/golusoris@<version>` + `go mod tidy` |

## Version

- Release builds set `main.version`, `main.commit`, `main.date` via
  `-ldflags -X` (`tools/.goreleaser.yml`). Rename var -> update both files.
- `main_test.go` builds binary through `internal/testbin` with release flags;
  asserts `--version` output, subcommand wiring, unknown-command exit 1.
- `scripts/ci/release-smoke.sh` runs `--version` on goreleaser linux/amd64
  output before draft publish.

## Internal layout

```text
cmd/golusoris/
├── main.go
└── internal/scaffold/
    ├── init.go   # init subcommand + file templates
    ├── add.go    # add subcommand + knownModules map
    └── bump.go   # bump subcommand (shells out to go get)
```
