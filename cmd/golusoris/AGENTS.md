# Agent guide — cmd/golusoris/

Scaffolder CLI built with `clikit/`. Wraps the three core subcommands.

## Usage

```sh
golusoris init myapp --module github.com/myorg/myapp
golusoris add db
golusoris add http
golusoris bump v0.5.0
```

## Subcommands

| Command | Description |
|---|---|
| `init <name>` | Scaffold new app directory with `go.mod` + `main.go` |
| `add <module>` | Print how to add a module (db, http, otel, cache, jobs, auth-oidc, authz, k8s) |
| `bump <version>` | Run `go get github.com/golusoris/golusoris@<version>` + `go mod tidy` |

## Internal layout

```
cmd/golusoris/
├── main.go
└── internal/scaffold/
    ├── init.go   # init subcommand + file templates
    ├── add.go    # add subcommand + knownModules map
    └── bump.go   # bump subcommand (shells out to go get)
```
