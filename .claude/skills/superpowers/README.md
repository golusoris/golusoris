---
name: superpowers
description: Meta-skills (TDD, systematic debugging, verification, subagent-driven dev, plan writing, brainstorming, code review) lifted from obra/superpowers. Use via `/superpowers:<skill-name>` or let the agent invoke by description.
---

# Superpowers skills (vendored from obra/superpowers)

These are **process skills**, not framework-specific skills. They govern how Claude Code approaches work: when to write a test first, how to debug systematically, what "done" actually means, how to dispatch subagents, etc.

The first-party skills in the sibling directory (`add-migration`, `wire-fx-module`, …) handle golusoris-specific tasks. These superpowers complement them — they are the general-purpose discipline layer.

## Picked skills

| Skill | Use when |
| --- | --- |
| `verification-before-completion` | About to claim work is done, fixed, or passing. No completion claims without fresh verification evidence. |
| `systematic-debugging` | Bug, test failure, or unexpected behavior. Find root cause before proposing fixes. |
| `test-driven-development` | Implementing a feature or bugfix. Write the failing test first, watch it fail, then make it pass. |
| `subagent-driven-development` | Executing a plan whose tasks are mostly independent — dispatch a fresh subagent per task with two-stage review. |
| `writing-plans` | Spec or requirements for a multi-step task, before touching code. |
| `executing-plans` | Plan document exists and you're about to start implementing it. |
| `brainstorming` | Creating a feature, building a component, adding functionality — explores intent before implementation. |
| `requesting-code-review` | Requesting review on your own changes. |
| `receiving-code-review` | Processing review feedback you got back. |
| `using-git-worktrees` | Creating an isolated worktree for a parallel branch of work with safety checks. |
| `finishing-a-development-branch` | Wrapping up a branch: ship options, PR hand-off, cleanup. |

## Attribution

Vendored from <https://github.com/obra/superpowers> (commit on `main` as of 2026-04-13). MIT-licensed; copyright (c) 2025 Jesse Vincent. See upstream `LICENSE` for the full text — the MIT license permits copying subject to retaining the copyright notice, which this README satisfies.

Divergence policy: these files are copies, not submodules. We do not auto-track upstream. When Claude's bump-golusoris skill cuts a new release, it can include "skills bumped" entries if the upstream files are refreshed. If you edit these files in-tree to fit golusoris conventions, note the divergence in your commit message (`Migration:` footer is overkill; a normal body paragraph suffices).
