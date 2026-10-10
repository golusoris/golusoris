#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Prints the newest published v* release tag other than <tag>, or nothing when there is none.
# Breaking entries and API changes are counted from it: a tag can exist without a release (#740).

set -euo pipefail

if (($# != 1)) || [[ -z "$1" ]]; then
	printf 'usage: %s <tag>\n' "${0##*/}" >&2
	exit 2
fi
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY names the repository whose releases are listed}"

RELEASE_TAG="$1" gh release list --repo "$GITHUB_REPOSITORY" --exclude-drafts \
	--exclude-pre-releases --limit 20 --json tagName \
	--jq '[.[].tagName | select(startswith("v") and . != env.RELEASE_TAG)][0] // ""'
