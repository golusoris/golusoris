<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/gitx

Bounded `git` runner + repository facts. Every invocation has a deadline
(`DefaultTimeout` 30 s when the ctx has none), NUL-checked arguments, and
output capped at `MaxOutput` (16 MiB) — a runaway `git log` fails closed
instead of eating memory. Sub-package `worktree/` manages per-task worktrees.
Capability key: `git.worktree`.

## Key API

| Symbol | Purpose |
|---|---|
| `New(dir, opts...)` | runner rooted at `dir`; `WithTimeout`, `WithBinary`, `WithMaxOutput` |
| `(*Runner).Run(ctx, args...)` / `Output` | raw stdout bytes / trimmed string; stderr folded into the error |
| `TopLevel` · `Head` · `Branch` · `RemoteURL(remote)` · `IsDirty` | common repository facts |
| `ValidRef(s)` | git-check-ref-format-style validation — use before passing any user-supplied ref |
| `ParseRemote(url)` | `owner, repo, ok` from https / ssh / scp-like remotes |
| `worktree.New(root, opts...)` | `WithDir` (default `.golusoris/worktrees`), `WithBranchPrefix` (default `wt/`), `WithRunner` |
| `(*worktree.Manager).Create/Remove/List/Prune/Path` | per-task worktree lifecycle; task ids `[A-Za-z0-9_-]{1,128}` |
| `worktree.ParseList(out)` | porcelain parser (bounded by `MaxPorcelainLines`) |

## Don't

- Don't call `os/exec` for git anywhere else in the fleet — use this runner so
  timeouts and output bounds are uniform.
- Don't pass unvalidated refs/remotes: a leading `-` is an option injection.
  `RemoteURL` and `worktree.Create` already reject them; keep it that way.
- Don't hold the `Manager` mutex across long operations of your own — it only
  serialises git calls.
