<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/testbin

Test helper: build main package, run binary, capture output + exit code.

## Contract

- `Build(tb, pkg, ldflags)` -> `go build -ldflags <ldflags>` into
  `tb.TempDir()`; `.exe` suffix on Windows. Build failure -> `tb.Fatalf`.
  No `-trimpath`; warm compile cache reuse.
- `Run(tb, bin, args...)` -> `Result{Stdout, Stderr, ExitCode}`. Non-zero exit
  -> `ExitCode`, test continues. Start failure or `Timeout` overrun -> `tb.Fatalf`.
- Tests only. Never import from non-test code.
