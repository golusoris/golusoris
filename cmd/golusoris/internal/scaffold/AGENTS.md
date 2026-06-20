# Agent guide — cmd/golusoris/internal/scaffold/

Implements the `golusoris` CLI subcommands (cobra, built via `clikit`).
Internal package — not importable by apps; consumed only by
`cmd/golusoris/main.go`, which assembles the root command.

## Commands

```go
scaffold.InitCmd()  // golusoris init <name> [--module path]  — scaffold a new app
scaffold.AddCmd()   // golusoris add [module]                 — print fx wiring snippet
scaffold.BumpCmd()  // golusoris bump [version]               — go get + go mod tidy
```

- **init** writes `go.mod` + `main.go` from `text/template` into `./<name>`;
  validates the name (rejects path/shell metacharacters), defaults the module
  path to `github.com/example/<name>`.
- **add** looks up a short name (`db`, `http`, `otel`, `cache`, `jobs`,
  `auth-oidc`, `authz`, `k8s`) in `knownModules` and prints the import + fx var;
  bare `add` lists them.
- **bump** shells `go get github.com/golusoris/golusoris@<version>` then
  `go mod tidy` and points at `docs/migrations/` for breaking-change notes.

## Notes

- Each command is built with `clikit.Command(use, short, clikit.WithRunE(...))`,
  not raw `&cobra.Command{}` — keep that for consistent help/error formatting.
- Keep `knownModules` in sync with the exported `golusoris.*` fx vars when
  modules are added or renamed.
- `exec` / `os.Create` `//nolint:gosec` sites are justified (operator-supplied
  CLI args, not user input) — preserve the justification if touched.
