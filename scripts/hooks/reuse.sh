#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: REUSE / SPDX compliance (LICENSING.md). Skips when the reuse tool is absent.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

# Prefer a standalone binary; fall back to the module of whichever python is present.
reuse_cmd=""
if command -v reuse >/dev/null 2>&1; then
  reuse_cmd="reuse"
else
  for py in python3 python; do
    if command -v "$py" >/dev/null 2>&1 && "$py" -c 'import reuse' >/dev/null 2>&1; then
      reuse_cmd="$py -m reuse"
      break
    fi
  done
fi
[ -n "$reuse_cmd" ] || skip "reuse is not on PATH — install with: pipx install reuse (or python -m pip install --user reuse); CI still runs reuse lint"

cd "$(git rev-parse --show-toplevel)"
$reuse_cmd lint --quiet || fail "REUSE lint failed — run: $reuse_cmd lint (see LICENSING.md)"
