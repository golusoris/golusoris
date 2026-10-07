<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — bootstrap

Lean service entry point. Holds `Core` (config, log, clock, id, validate,
crypto) and `HTTP` (router, server) fx groupings. Root `golusoris.Core` and
`golusoris.HTTP` alias these values; one definition only.

## Rules

- Import only `core/*`, `httpx/router`, `httpx/server`. `TestImportGraphStaysLean`
  fails on any other framework package and on the root package.
- New grouping here only when nearly every service needs it; else apps import
  sub-package `Module` directly.
- Never import root `golusoris`: root imports this package.

## Tests

- `TestImportGraphStaysLean`: `go list -deps` allowlist; skips when `go` missing.
- `TestGroupingsValidate`: `fx.ValidateApp` over `Core` + `HTTP`.
