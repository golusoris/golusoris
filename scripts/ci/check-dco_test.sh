#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly CHECK="$SCRIPT_DIR/check-dco.sh"
repo="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-dco.XXXXXX")"
readonly repo
trap 'rm -rf "$repo"' EXIT

git_in() {
	git -C "$repo" -c commit.gpgSign=false "$@"
}

commit_as() {
	local name="$1" email="$2" message="$3"
	git_in -c user.name="$name" -c user.email="$email" commit -q --allow-empty -m "$message"
	git_in rev-parse HEAD
}

expect_failure() {
	if (cd "$repo" && bash "$CHECK" "$@") >/dev/null 2>&1; then
		printf 'expected DCO failure for %s\n' "$*" >&2
		exit 1
	fi
}

git_in init -q
base="$(commit_as base base@example.invalid 'base')"
signed="$(commit_as Dev dev@example.invalid "$(printf 'feat: x\n\nSigned-off-by: Dev <dev@example.invalid>')")"
(cd "$repo" && bash "$CHECK" "$base" "$signed") >/dev/null

bot="$(commit_as 'github-actions[bot]' '41898282+github-actions[bot]@users.noreply.github.com' 'chore: release main')"
(cd "$repo" && bash "$CHECK" "$base" "$bot") >/dev/null

human="$(commit_as Dev dev@example.invalid 'fix: y')"
expect_failure "$base" "$human"
expect_failure "$bot" "$human"

printf 'DCO check tests passed\n'
