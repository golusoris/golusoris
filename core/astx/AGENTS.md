<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/astx

Shared Go-source analysis primitives for tooling (praetor scanners,
`golusoris bump` codemods, `needs` migration). Everything is bounded: walks
have a file budget, reads have size limits, parsing uses `go/parser` — never
regexes over source text. Capability key: `ast.analyzer`.

## Key API

| Symbol | Purpose |
|---|---|
| `Walk(ctx, root, WalkOptions, fn)` | every `.go` file (tests optional); skips `vendor`, `testdata`, `node_modules`, `.*`, `_*`; `MaxFiles` budget (50 000) |
| `Imports(path)` · `IsThirdParty(path, module)` | import list; stdlib/self vs third-party split |
| `Resolve(path, mapping)` | longest-prefix import rewrite at `/` boundaries |
| `RewriteImports(src, mapping)` · `RewriteImportsFile(path, mapping)` | AST-based rewrite, aliases + comments preserved, gofmt output |
| `FuncMetrics(path)` → `[]FuncMetric` | per-function `Lines`, `Cyclomatic`, `Statements`, `Params` for HISS-04 / rule-4 gates |
| `ParseGoMod(path)` → `*GoMod` · `Direct()` | go.mod via `golang.org/x/mod/modfile` |
| `ReadFileBounded(path, limit)` | size-checked read used by the above |

## Don't

- Don't string-replace import paths (`strings.ReplaceAll` on `"old"`) — it
  also rewrites string literals and comments. Use `RewriteImports`.
- Don't hand-parse `go.mod` lines; `ParseGoMod` handles blocks, comments,
  `// indirect`, and retract/replace directives correctly.
- Don't run `Walk` without a cancellable context on user-supplied roots.
