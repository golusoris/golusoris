#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# GitHub's image proxy drops badges that shields.io computes per request, so the README
# keeps one static release badge that release-please rewrites and no computed badge in its top block.
#   usage: scripts/ci/readme-badges.sh [README]
set -euo pipefail

README="${1:-README.md}"
if [ ! -f "$README" ]; then
	echo "error: $README not found" >&2
	exit 2
fi

status=0
release_re='img[.]shields[.]io/static/v1[?]label=release&message=v[0-9]+[.][0-9]+[.][0-9]+[^&]*&.*<!-- x-release-please-version -->[[:space:]]*$'
count="$(awk -v re="$release_re" '$0 ~ re { n++ } END { print n + 0 }' "$README")"
if [ "$count" -ne 1 ]; then
	echo "error: $README needs exactly one static release badge ending in <!-- x-release-please-version -->, found $count" >&2
	status=1
fi
computed_re='img\.shields\.io/github/|api\.scorecard\.dev/projects/.*/badge|img\.shields\.io/coderabbit/'
block="$(awk '/<!-- README-STANDARD badge block/ { on = 1 } on && /^[[:space:]]*$/ { exit } on' "$README")"
if [ -z "$block" ]; then
	echo "error: $README has no <!-- README-STANDARD badge block --> comment" >&2
	status=1
elif grep -nE "$computed_re" <<< "$block" >&2; then
	echo "error: $README uses a computed badge source; use a static badge" >&2
	status=1
fi
exit "$status"
