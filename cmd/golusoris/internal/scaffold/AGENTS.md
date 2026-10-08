<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — cmd/golusoris/internal/scaffold/

Implements `golusoris` CLI subcommands (cobra, built via `clikit`).
Internal package — not importable by apps; consumed only by
`cmd/golusoris/main.go`, which assembles root command.

## Commands

```go
scaffold.InitCmd()  // golusoris init <name> [--module path]  — scaffold a new app
scaffold.AddCmd()   // golusoris add [module]                 — print fx wiring snippet
scaffold.BumpCmd()  // golusoris bump [version]               — go get + go mod tidy
```

- **init** writes `go.mod` + `main.go` from `text/template` into `./<name>`;
 validates name (rejects path/shell metacharacters), defaults module
 path to `github.com/example/<name>`.
- **add** looks up short name (`db`, `http`, `otel`, `cache`, `jobs`,
 `auth-oidc`, `authz`, `k8s`) in `knownModules` and prints import + fx var;
 bare `add` lists them.
- **bump** shells `go get github.com/golusoris/golusoris@<version>` then
 `go mod tidy` and points at `docs/migrations/` for breaking-change notes.
 Each subprocess gets 5-minute ceiling; earlier caller deadline/cancellation
 wins. Captured failure diagnostics cap at 64 KiB. Post-cancel wait caps at 1s.

## Notes

- Each command is built with `clikit.Command(use, short, clikit.WithRunE(...))`,
 not raw `&cobra.Command{}` — keep that for consistent help/error formatting.
- Keep `knownModules` in sync with exported `golusoris.*` fx vars when
 modules are added or renamed.
- `exec` / `os.Create` `//nolint:gosec` sites are justified (operator-supplied
 CLI args, not user input) — preserve justification if touched.
