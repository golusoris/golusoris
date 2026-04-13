# Claude Code guide — golusoris

> Claude Code-specific guide. For cross-tool conventions read [AGENTS.md](AGENTS.md) first; this file extends it.

## Skills available

Located in `.claude/skills/`:

| Skill | When to use |
|---|---|
| `wire-fx-module` | Adding a new opt-in fx module to the framework |
| `scaffold-ogen-handler` | Generating an ogen handler stub from an operationId |
| `add-river-worker` | Adding a river job worker (registered in fx) |
| `add-migration` | Creating a timestamped golang-migrate up/down pair |
| `bump-golusoris` | Bumping golusoris in a downstream app + applying codemods from migration notes |

Invoke via `/<skill-name>` in Claude Code.

## Hooks active

Located in `.claude/hooks/`:

- Touching `**/jobs/*.go` auto-loads `docs/upstream/river/` + `jobs/AGENTS.md`
- Touching `**/migrations/*.sql` auto-loads `docs/upstream/golang-migrate/` + the project's existing migrations summary
- Touching `**/api/*.go` (ogen) auto-loads `docs/upstream/ogen/` + the OpenAPI spec
- Pre-commit: runs `make ci` (lint + sec + test)

## Tone

- Be terse. No preamble.
- When changing public API: write the `Migration:` footer in the commit body, with before/after Go snippets.
- When adding a dependency: state which awesome-go alternatives you considered and why this one wins.
- Never add init() side effects. Always use fx lifecycle.

## Don't

- Don't use `time.Now()` outside `clock/`. Use `clock.Now(ctx)`.
- Don't `fmt.Println` — use the slog handler from `log/`.
- Don't add features beyond what the task requires (per global Claude Code guidelines).
- Don't write multi-paragraph comments. One-liner WHY comments only.
- Don't create new markdown docs unless explicitly asked.

## Project state

- This is pre-alpha (Step 1: Skeleton + Core).
- Not yet pushed to `github.com/golusoris/golusoris` (org needs to be registered first).
- See [.workingdir/PLAN.md](.workingdir/PLAN.md) for the full plan.
