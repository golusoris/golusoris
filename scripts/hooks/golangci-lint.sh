#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: golangci-lint (tools/golangci.yml) on the packages of the staged .go files.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

files=$(go_files "$@")
[ -n "$files" ] || skip "no staged Go files"
need_tool golangci-lint "see https://golangci-lint.run/docs/welcome/install/ (CI pins v2.13.2)"

config="$(git rev-parse --show-toplevel)/tools/golangci.yml"
run_per_module "$files" golangci-lint run --config "$config" --timeout=5m ||
  fail "golangci-lint reported issues"
