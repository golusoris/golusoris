#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# check-dco.sh BASE HEAD fails when a human-authored commit in BASE..HEAD has no
# Signed-off-by trailer. Commits authored by a GitHub App bot (release-please,
# Renovate) certify nothing and are skipped, whoever triggered the run.

set -euo pipefail

readonly bot_author_suffix='[bot]@users.noreply.github.com'

main() {
	local base="${1:?usage: check-dco.sh BASE HEAD}"
	local head="${2:?usage: check-dco.sh BASE HEAD}"
	local commits sha author missing=0
	commits="$(git rev-list --no-merges "$base..$head")"
	for sha in $commits; do
		author="$(git show -s --format=%ae "$sha")"
		if [[ "$author" == *"$bot_author_suffix" ]]; then
			printf 'commit %s: authored by %s, not a human contribution\n' "$sha" "$author"
			continue
		fi
		if ! git show -s --format=%B "$sha" | grep -qE '^Signed-off-by: .+ <.+@.+>$'; then
			printf '::error::commit %s lacks a Signed-off-by trailer (use git commit -s)\n' "$sha"
			missing=1
		fi
	done
	return "$missing"
}

main "$@"
