#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: go vet on the packages of the staged .go files.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

files=$(go_files "$@")
[ -n "$files" ] || skip "no staged Go files"
need_tool go "https://go.dev/dl/"

run_per_module "$files" go vet || fail "go vet reported issues"
