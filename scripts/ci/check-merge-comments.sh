#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# check-merge-comments.sh BASE HEAD fails when any commit in BASE..HEAD keeps git's # Conflicts: comment block.

set -euo pipefail

main() {
	if [[ "$#" -ne 2 || -z "$1" || -z "$2" || "$1" == -* || "$2" == -* ]]; then
		printf 'usage: check-merge-comments.sh BASE HEAD\n' >&2
		return 3
	fi
	local base="$1"
	local head="$2"
	local commits sha commit_msg status=0 grep_rc
	if ! commits="$(git rev-list "$base..$head")"; then
		return 3
	fi
	if [[ -z "$commits" ]]; then
		return 0
	fi
	while IFS= read -r sha; do
		if ! commit_msg="$(git show -s --format=%B "$sha" </dev/null)"; then
			return 3
		fi
		# Strip CRs so Windows-authored commit messages match the line-end anchor.
		commit_msg="${commit_msg//$'\r'/}"
		grep_rc=0
		grep -qE '^# Conflicts:$' <<<"$commit_msg" || grep_rc="$?"
		if ((grep_rc == 0)); then
			printf "::error::commit %s keeps git's conflict comment lines (# Conflicts:); reword the message with editor cleanup (strip mode)\n" "$sha"
			status=1
		elif ((grep_rc > 1)); then
			return 3
		fi
	done <<<"$commits"
	return "$status"
}

main "$@"
