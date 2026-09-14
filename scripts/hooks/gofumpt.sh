#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: fail when any staged .go file is not gofumpt-formatted.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

files=$(go_files "$@")
[ -n "$files" ] || skip "no staged Go files"
need_tool gofumpt "go install mvdan.cc/gofumpt@latest"

# shellcheck disable=SC2086 # file list is intentionally word-split
unformatted=$(gofumpt -l $files)
[ -z "$unformatted" ] || fail "not gofumpt-formatted (run: gofumpt -w <file>):
$unformatted"
