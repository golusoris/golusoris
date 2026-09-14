#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# commit-msg: Conventional Commits subject + Signed-off-by trailer (DCO). See CONTRIBUTING.md.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

msgfile=${1:?usage: commit-msg.sh <commit-message-file>}
# Drop comment lines and CRs (Windows editors) before inspecting the message.
body=$(tr -d '\r' < "$msgfile" | grep -vE '^#' || true)
subject=$(printf '%s\n' "$body" | grep -m1 -E '.' || true)
[ -n "$subject" ] || fail "empty commit message"

# git-generated subjects and to-be-squashed fixups are exempt (CI's DCO check ignores merges).
case "$subject" in
  "Merge "* | "Revert \""* | "fixup! "* | "squash! "* | "amend! "*) exit 0 ;;
esac

types='feat|fix|docs|chore|refactor|test|perf|ci|build|revert'
printf '%s\n' "$subject" | grep -qE "^($types)(\([^)]+\))?!?: .+" ||
  fail "subject is not a Conventional Commit: '$subject'
expected: <type>(<scope>): <description>   types: ${types//|/ }"

printf '%s\n' "$body" | grep -qE '^Signed-off-by: .+ <.+@.+>$' ||
  fail "missing Signed-off-by trailer — commit with: git commit -s (Developer Certificate of Origin)"
