# Claude Code hooks — golusoris

Working hook scripts wired from [`../settings.json`](../settings.json). Each
script reads the Claude Code hook JSON envelope on stdin, inspects the tool
input, and either allows (exit 0), blocks with a rejection reason (exit 2), or
runs a post-action formatter.

The bar for adding rules here is: **would it save an edit cycle compared to
catching the same thing in CI?** `golangci-lint` is authoritative — these
hooks exist so the agent doesn't spend a round trip on rules it already knows.

## Active hooks

| Hook | Event | Matcher | Exit 2 triggers |
|---|---|---|---|
| [`guard-bash.sh`](guard-bash.sh) | PreToolUse | `Bash` | `--no-verify`, `--no-gpg-sign`, force-push to main/master, `reset --hard main`, `rm -rf .git`, `rm -rf .workingdir` |
| [`guard-go-edit.sh`](guard-go-edit.sh) | PreToolUse | `Edit\|Write` on `*.go` | `time.Now()` outside `clock/`, `fmt.Print*` in production code, `//nolint` without justification |
| [`format-go-write.sh`](format-go-write.sh) | PostToolUse | `Edit\|Write` on `*.go` | (never blocks) runs `gofumpt -w` + `gci write` when those tools are on PATH |

### guard-go-edit.sh — path exemptions

The Go rules above do **not** fire for:

- `*_test.go`
- `*/example_*.go`
- `*/testutil/*`
- `*/examples/*`
- `*/docs/*`

Also, `time.Now()` is allowed inside `clock/` itself (that's the one place
the raw call may live).

## Why these three rules

- **`time.Now()` ban** — every call that reads the wall clock outside `clock/`
  breaks deterministic tests. The codebase is uniform on `clock.Now(ctx)` /
  injected `clockwork.Clock`. Catching this at edit-time avoids CI round-trips.
- **`fmt.Print*` ban** — production logs go through `log/`'s slog handler so
  they pick up OTel trace context and structured fields. `fmt.Println` bypasses
  that and the output is lost in a container.
- **`//nolint` needs justification** — golangci-lint's `nolintlint` enforces
  this in CI. Catching it here saves one push + wait cycle.

## Smoke-testing a hook

Each script reads the hook JSON envelope on stdin. To dry-run locally:

```bash
# Block test — guard-bash
printf '%s' '{"tool_input":{"command":"git push --force origin main"}}' \
  | .claude/hooks/guard-bash.sh; echo "exit=$?"

# Block test — guard-go-edit
printf '%s' '{"tool_name":"Edit","tool_input":{"file_path":"foo.go","new_string":"time.Now()"}}' \
  | .claude/hooks/guard-go-edit.sh; echo "exit=$?"

# Format test — format-go-write (no-op if gofumpt/gci aren't on PATH)
printf '%s' '{"tool_input":{"file_path":"clock/clock.go"}}' \
  | .claude/hooks/format-go-write.sh; echo "exit=$?"
```

A blocking hook returns exit code 2 and prints its reason to stderr; a passing
hook returns 0 silently.

## Not yet implemented (deferred)

The old stub doc also mentioned per-glob **context auto-loading** (e.g.
`**/jobs/*.go` → `docs/upstream/river/`). Those are documented in
[`../../CLAUDE.md`](../../CLAUDE.md) and [`../../AGENTS.md`](../../AGENTS.md)
as recommended reading; they're not wired as hooks yet because Claude Code's
UserPromptSubmit hook would need to grep the pending tool calls, which is
awkward. Revisit after v0.1.0 if Claude ignores the AGENTS.md pointers in
practice.

## Settings files — tracked vs personal

- [`../settings.json`](../settings.json) — **team-shared**, checked in. This
  is where the hook wiring lives.
- `../settings.local.json` — **personal**, gitignored. User-specific overrides
  and API key paths go here.
- `../scheduled_tasks.lock` — runtime lockfile, gitignored.

If you edit `settings.local.json` and it starts showing up in `git status`,
check `.gitignore`: the entries `.claude/settings.local.json` and
`.claude/scheduled_tasks.lock` should already be there.
