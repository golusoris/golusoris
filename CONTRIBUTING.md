# Contributing

## Conventional commits

All commits and PR titles MUST follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

[optional body]

[optional footer(s)]
```

Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`.

Scopes are subpackage names: `feat(jobs):`, `fix(db/pgx):`, `chore(tools):`.

## Breaking changes

Append `!` to type and add a `BREAKING CHANGE:` footer:

```
feat(auth)!: rename SessionStore to Sessions

BREAKING CHANGE: SessionStore is now Sessions; rename all references.

Migration:
  // before
  store := auth.NewSessionStore(db)
  // after
  store := auth.NewSessions(db)
```

The `Migration:` footer is **required** for breaking changes. CI fails without it. The footer is auto-stitched into `docs/migrations/vX.Y.Z.md`.

## Licensing and the Developer Certificate of Origin

Contributions are accepted under the licence of the material they touch —
`EUPL-1.2` for code, `CC-BY-SA-4.0` for prose (see [LICENSING.md](LICENSING.md)).
Every commit must carry a `Signed-off-by:` trailer certifying the
[Developer Certificate of Origin](https://developercertificate.org/):

```bash
git commit -s -m "feat(scope): subject"
```

CI rejects pull requests with unsigned commits. New Go files start with the
SPDX header (`goheader` lint enforces it):

```go
// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2
```

## CI gates

Every PR runs:
- `make lint` — golangci-lint (full set incl. gosec, gocritic, errorlint)
- `make vuln` — govulncheck
- `make test` — `go test -race -count=1`
- `apidiff` vs the previous tagged release — fails on undeclared API breakage
- `reuse lint` — REUSE/SPDX compliance
- DCO — `Signed-off-by:` on every commit
- `capabilities.yaml` drift guard — every package is in the capability contract

## Local dev

```bash
make dev    # air hot-reload (when implemented)
make ci     # full local CI (root module)
make verify-all  # root + core + governance gates (what CI runs)
make gen    # sqlc / ogen / mockery codegen
```

## Pre-commit hooks

```bash
pre-commit install
```

Hooks: `gofumpt`, `golangci-lint`, `gitleaks`, conventional-commit check.
