<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/mutation/

Helpers for mutation testing via `avito-tech/go-mutesting`: run it against package (or explicit files), parse score, assert minimum. Stateless test
utility — **no fx wiring**. Import directly from `_test.go` files.

## API

```go
r := mutation.Run(ctx, t, "github.com/example/app/parser") // or RunFiles(ctx, t, files...)
mutation.AssertMinScore(t, r, 0.80)                        // require ≥80% mutation score
// Report{ Killed, Total int; Score float64 }  // Score = Killed/Total in [0,1]
```

`Run`/`RunFiles` shell out to `go-mutesting` binary; non-zero exit (normal
when mutants survive) is treated as soft signal — output is parsed regardless.
`AssertMinScore` is no-op (logs only) when `Total == 0`.

## Notes

- `go-mutesting` must be installed separately and on `PATH`; both runners
 `t.Skip` when binary is absent:
 `make tools-bootstrap` installs the reviewed repository pin.
- package/file args feed `exec.CommandContext` — they come from trusted test
 code (`//nolint:gosec G204`); do not pass untrusted input.
- Score is scraped from go-mutesting's summary line via regex; format change
 upstream yields zero `Report`.
