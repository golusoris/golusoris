#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Planted cases for scripts/ci/readme-badges.sh, so a computed or unmarked badge fails here, not on github.com.
#   usage: scripts/ci/readme-badges_test.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

RELEASE='[![Release](https://img.shields.io/static/v1?label=release&message=v1.2.3&style=for-the-badge)](https://example.org/releases) <!-- x-release-please-version -->'

# check NAME WANT_RC LINE...
check() {
	local name="$1" want="$2" rc=0
	shift 2
	printf '%s\n' '<!-- README-STANDARD badge block -->' "$@" > "$WORK/README.md"
	bash "$HERE/readme-badges.sh" "$WORK/README.md" > "$WORK/out" 2>&1 || rc=$?
	if [ "$rc" -ne "$want" ]; then
		printf 'FAIL %s: exit %s, want %s\n' "$name" "$rc" "$want" >&2
		cat "$WORK/out" >&2
		exit 1
	fi
	echo "ok   $name: exit $rc"
}

check static-release 0 "$RELEASE"
check prerelease 0 "${RELEASE/v1.2.3/v1.2.3-rc.1}"
check missing-marker 1 "${RELEASE% <!-- x-release-please-version -->}"
check no-release 1 'plain text'
check two-release 1 "$RELEASE" "$RELEASE"
check computed-release 1 "$RELEASE" '[![R](https://img.shields.io/github/v/release/o/r)](x)'
check computed-scorecard 1 "$RELEASE" '[![S](https://api.scorecard.dev/projects/github.com/o/r/badge)](x)'
check computed-below-block 0 "$RELEASE" '' '[![G](https://img.shields.io/github/go-mod/go-version/o/r)](x)'
check computed-coderabbit 1 "$RELEASE" '[![C](https://img.shields.io/coderabbit/prs/github/o/r)](x)'
printf '%s\n' "$RELEASE" > "$WORK/README.md"
rc=0
bash "$HERE/readme-badges.sh" "$WORK/README.md" > /dev/null 2>&1 || rc=$?
[ "$rc" -eq 1 ]
echo "ok   no-badge-block: exit $rc"
rm -f "$WORK/README.md"
rc=0
bash "$HERE/readme-badges.sh" "$WORK/README.md" > /dev/null 2>&1 || rc=$?
[ "$rc" -eq 2 ]
echo "ok   absent-file: exit $rc"
bash "$HERE/readme-badges.sh" "$HERE/../../README.md"
echo "ok   repository README"
