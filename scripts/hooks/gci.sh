#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: fail when staged .go imports are not grouped per .golangci.yml (gci).
set -euo pipefail
. "$(dirname "$0")/lib.sh"

files=$(go_files "$@")
[ -n "$files" ] || skip "no staged Go files"
need_tool golangci-lint "run make tools-bootstrap"

config="$(git rev-parse --show-toplevel)/.golangci.yml"
# shellcheck disable=SC2086 # file list is intentionally word-split
golangci-lint fmt --config "$config" --enable gci --diff $files ||
  fail "imports not gci-grouped (run: golangci-lint fmt --enable gci <file>)"
