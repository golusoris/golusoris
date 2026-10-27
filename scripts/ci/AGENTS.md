<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — scripts/ci/

CI verification drivers, scripts, policy tests.

## Tools

| Script | Purpose |
| --- | --- |
| `ci_scope.py` | Diff-aware CI scope calculator (`modules`, `files`, `any`) |

## Operational Rules

- Stdlib only, Python 3.12+, Linux/macOS/Windows portable.
- `CI_SCOPE_BASE` empty: full mode; base set: diff mode.
- Exit codes: 0 ok or match, 1 `any` found no match, 3 error (fail closed); callers treat every other code as failure.
