# Claude Code hooks — golusoris

These hooks run automatically when Claude Code edits specific files.

## Context auto-loading hooks

| Glob | Loaded context |
|---|---|
| `**/jobs/*.go` | `docs/upstream/river/` + `jobs/AGENTS.md` |
| `**/migrations/*.sql` | `docs/upstream/golang-migrate/` + project migrations summary |
| `**/api/*.go` (ogen) | `docs/upstream/ogen/` + the OpenAPI spec |
| `**/db/pgx/*.go` | `docs/upstream/pgx/` |
| `**/config/*.go` | `docs/upstream/koanf/` |

## Pre-commit hook

Runs `make ci` (lint + sec + test) before every commit.
If it fails, fix the issue — never bypass with `--no-verify`.

## Conventions

- `//nolint:<linter>` must always have a justification comment.
- `time.Now()` outside `clock/` is banned — use `clock.Now(ctx)` or inject `clockwork.Clock`.
- `fmt.Println` is banned — use the slog handler from `log/`.
