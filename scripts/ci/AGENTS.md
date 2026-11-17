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
| `cmd/workflow_actor_parser/` | Bounded YAML scalar and actionlint expression parser; actor-policy second barrier |

Scope: stdlib rule below covers Python tools in this table; existing shell policy tests (`*_test.sh`) run via `make ci-policy-test`, except four with own make targets: `gci-hook-test`, `go-modules-test`, `helm-backup-restore`, `text-register-policy`.

## Operational Rules

- Stdlib only, Python 3.12+, Linux/macOS/Windows portable.
- `CI_SCOPE_BASE` empty: full mode; base set: diff mode.
- Portability gate: new untracked, non-ignored paths after module phases -> failure.
- Exit codes: 0 ok or match, 1 `any` found no match, 3 error (fail closed); callers treat every other code as failure.
