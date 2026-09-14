#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: compiled vendor agent-context files (CLAUDE.md, .codex/, …) must match AGENTS.md.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

need_tool standardsctl "go install github.com/cordanallm/praetor/cmd/standardsctl@latest (drift is still caught by make verify-all / CI)"

cd "$(git rev-parse --show-toplevel)"
standardsctl compile-context --verify ||
  fail "compiled agent context drifted from AGENTS.md — run: standardsctl compile-context"
