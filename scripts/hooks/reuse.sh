#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: REUSE / SPDX compliance through the immutable repository authority.
set -euo pipefail
# shellcheck source=scripts/hooks/lib.sh
. "$(dirname "$0")/lib.sh"

root="$(git rev-parse --show-toplevel)"
bash "$root/scripts/ci/reuse-lint.sh" || \
  fail "REUSE lint failed — run: make reuse-lint (see LICENSING.md)"
