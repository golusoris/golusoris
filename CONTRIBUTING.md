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

## Git hooks (lefthook)

Local gates run through [lefthook](https://github.com/evilmartians/lefthook)
— configuration in [`lefthook.yml`](lefthook.yml), one bash script per check
under [`scripts/hooks/`](scripts/hooks/). Install once per clone:

```bash
go install github.com/evilmartians/lefthook@latest
lefthook install          # writes .git/hooks/{pre-commit,commit-msg,pre-push}
```

| Hook | Runs |
| --- | --- |
| `pre-commit` (parallel, staged `*.go` only) | `gofumpt -l`, `gci list`, `golangci-lint run --config .golangci.yml` and `go vet` on the packages of the staged files; `standardsctl compile-context --verify`, `reuse lint`, `gitleaks git --staged` |
| `commit-msg` | Conventional Commits subject (`<type>(<scope>): <description>`) and the DCO `Signed-off-by:` trailer |
| `pre-push` | `go build ./...` + `go test -short ./...` (no `-race`) in the root and `core/` modules |

A check whose tool is not on PATH (`gofumpt`, `gci`, `golangci-lint`,
`standardsctl`, `reuse`, `gitleaks`) skips with an install hint instead of
failing — the hooks never require Python. CI (`make verify-all`) remains the
authoritative gate, so a skipped local check is still enforced on the PR.

Dry-run without committing:

```bash
lefthook run pre-commit                      # staged files
lefthook run pre-commit --file path/to/x.go  # a specific file
lefthook run commit-msg .git/COMMIT_EDITMSG
```
