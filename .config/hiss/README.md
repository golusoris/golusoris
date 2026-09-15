<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# `.config/hiss/` — the HISS-20 enforcement coverage catalogue and its corpus

Every claim in [`coverage.yaml`](coverage.yaml) is replayed against `testdata/` by
`praetorctl hiss coverage --verify` (`make hiss-coverage`, the lefthook pre-commit job
`hiss-coverage`, and `make verify-all`). A claim is only as good as the fixture that
demonstrates it.

## Layout

```text
.config/hiss/testdata/HISS-NN/<language>/
  positive/   code that violates the invariant and that the claimed mechanism reports
  negative/   legitimate code the mechanism must NOT report
  gap/        real violations that go undetected — what the claim does not cover
```

`<language>` is `go`, `python` or `c`, matching the `language:` key of the claim. Only
languages this repository actually contains are declared.

## What the verifier checks

Each fixture is copied alone into a temporary directory and scanned in isolation, so a
finding can never be attributed to a neighbouring file.

- A claim whose `runner` is omitted is decided by the HISS scanner (`praetorctl audit`), and
  the verifier replays it: every `positive/` fixture must be reported for that rule id, every
  `negative/` and `gap/` fixture must not.
- A claim naming any other `runner` — `golangci-lint`, `gitleaks`, `baseline`, `dedupe`, `ci`,
  `compile-context`, `hiss-coverage`, `state` — is **delegated**. The verifier does not run
  that tool, so it checks the *attribution* instead: the HISS scanner must report none of the
  fixtures. A rule credited to semgrep while the scanner is quietly deciding it names the
  wrong mechanism, and that is what this direction catches.

For a delegated claim the enforcement evidence is the measurement quoted in its `rationale`,
taken from running the named tool against the named fixture. Read it before trusting the
state.

## Conventions

- Fixtures are ordinary source files with no build tag. The Go toolchain never compiles them:
  `go build`, `go vet` and `golangci-lint` all skip any directory named `testdata`, and the
  `go_files()` helper in `scripts/hooks/lib.sh` filters `testdata/` out of the gofumpt, gci,
  govet and golangci-lint pre-commit hooks. `praetorctl audit` and `praetorctl dedupe scan`
  skip it too, which is why a deliberately duplicated pair under `HISS-19/go/positive/` does
  not count as repository debt.
- Every fixture carries the repository's SPDX header (`LICENSING.md`).
- Go fixtures use `package p` so any two of them can share a directory.
- One fixture demonstrates one shape. Splitting a file is cheaper than debugging why a
  two-shape fixture reported only once.

## Adding or moving a fixture

See the section *Updating the HISS-20 enforcement coverage catalogue* in
[`AGENTS.md`](../../AGENTS.md). The short version: when a rule gains coverage, move the
fixture from `gap/` to `positive/` and update the claim in the same PR as the mechanism — and
never weaken a fixture to make a stale claim pass.
