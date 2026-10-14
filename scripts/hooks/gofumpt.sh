#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: fail when any staged .go file is not gofumpt-formatted.
set -euo pipefail
# shellcheck source=scripts/hooks/lib.sh
. "$(dirname "$0")/lib.sh"

declare -a files=()
mapfile -t files < <(go_files "$@")
((${#files[@]} > 0)) || skip "no Go files"
# One implementation for hook, Make and CI (HISS-19).
exec bash "$(dirname "$0")/../ci/check-gofumpt.sh" "${files[@]}"
