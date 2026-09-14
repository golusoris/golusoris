#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: fail when staged .go imports are not grouped per tools/golangci.yml (gci).
set -euo pipefail
. "$(dirname "$0")/lib.sh"

files=$(go_files "$@")
[ -n "$files" ] || skip "no staged Go files"
need_tool gci "go install github.com/daixiang0/gci@latest"

# Sections mirror linters.settings.gci in tools/golangci.yml.
sections=(-s standard -s default -s 'prefix(github.com/golusoris/golusoris)' --custom-order)
# shellcheck disable=SC2086 # file list is intentionally word-split
unsorted=$(gci list "${sections[@]}" $files)
[ -z "$unsorted" ] || fail "imports not gci-grouped (run: gci write ${sections[*]} <file>):
$unsorted"
