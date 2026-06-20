# Agent guide — testutil/mutation/

Helpers for mutation testing via `avito-tech/go-mutesting`: run it against a
package (or explicit files), parse the score, assert a minimum. Stateless test
utility — **no fx wiring**. Import directly from `_test.go` files.

## API

```go
r := mutation.Run(ctx, t, "github.com/example/app/parser") // or RunFiles(ctx, t, files...)
mutation.AssertMinScore(t, r, 0.80)                        // require ≥80% mutation score
// Report{ Killed, Total int; Score float64 }  // Score = Killed/Total in [0,1]
```

`Run`/`RunFiles` shell out to the `go-mutesting` binary; a non-zero exit (normal
when mutants survive) is treated as a soft signal — output is parsed regardless.
`AssertMinScore` is a no-op (logs only) when `Total == 0`.

## Notes

- `go-mutesting` must be installed separately and on `PATH`; both runners
  `t.Skip` when the binary is absent:
  `go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest`.
- The package/file args feed `exec.CommandContext` — they come from trusted test
  code (`//nolint:gosec G204`); do not pass untrusted input.
- Score is scraped from go-mutesting's summary line via regex; a format change
  upstream yields a zero `Report`.
