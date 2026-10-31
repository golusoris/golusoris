#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

# PostToolUse hook for Edit / Write on *.go files.
#
# Runs the formatter stack (gofumpt + goimports) on the touched file so
# the next Read shows canonical formatting. Silent on success; errors
# print to stderr but never block (exit 0 always) — the formatter isn't
# authoritative, golangci-lint in CI is.
#
# Wired from .claude/settings.json under PostToolUse / matcher: "Edit|Write".

set -uo pipefail

payload=$(cat)

if ! command -v jq >/dev/null 2>&1; then
  exit 0
fi

file_path=$(printf '%s' "$payload" | jq -r '.tool_input.file_path // ""')

[ -z "$file_path" ] && exit 0
[[ "$file_path" != *.go ]] && exit 0
[ ! -f "$file_path" ] && exit 0

# gofumpt is the stricter superset; gci (via golangci-lint and the shared
# .golangci.yml sections) handles import grouping. Either is optional.
if command -v gofumpt >/dev/null 2>&1; then
  gofumpt -w "$file_path" 2>/dev/null || true
fi
if command -v golangci-lint >/dev/null 2>&1; then
  config="$(git -C "$(dirname "$file_path")" rev-parse --show-toplevel 2>/dev/null)/.golangci.yml"
  [ -f "$config" ] && golangci-lint fmt --config "$config" --enable gci "$file_path" 2>/dev/null || true
fi

exit 0
