<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Package-local agent context

A second, hand-written agent-context file that no compilation step produces and
no verification step compares against the canonical root AGENTS.md.

`standardsctl compile-context --verify` renders the root `AGENTS.md` into the
vendor targets it knows about (`CLAUDE.md`, `.cursor/`, `.github/`,
`.windsurfrules`, `.gemini/`, `.codex/`) and compares those. A nested
`AGENTS.md` further down the tree is neither an input nor a target, so it can
contradict the canonical source indefinitely without the gate noticing. The
repository already contains such files, `pkg/sockmap/AGENTS.md` among them.
