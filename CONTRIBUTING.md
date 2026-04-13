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

## CI gates

Every PR runs:
- `make lint` — golangci-lint (full set incl. gosec, gocritic, errorlint)
- `make vuln` — govulncheck
- `make test` — `go test -race -count=1`
- `apidiff` vs the previous tagged release — fails on undeclared API breakage

## Local dev

```bash
make dev    # air hot-reload (when implemented)
make ci     # full local CI
make gen    # sqlc / ogen / mockery codegen
```

## Pre-commit hooks

```bash
pre-commit install
```

Hooks: `gofumpt`, `golangci-lint`, `gitleaks`, conventional-commit check.
