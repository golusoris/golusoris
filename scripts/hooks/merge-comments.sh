#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# pre-push: check every pushed ref range against origin/main for leftover # Conflicts: comment lines.

set -euo pipefail

readonly max_refs=4096

check_ref_line() {
	local check_script="$1" line="${2%$'\r'}"
	local -a fields=()
	IFS=$' \t' read -r -a fields <<<"$line"
	if ((${#fields[@]} != 4)); then
		printf 'merge-comments: malformed pre-push line (expected 4 fields, got %d)\n' "${#fields[@]}" >&2
		return 3
	fi
	local local_oid="${fields[1]}"
	if [[ ! "$local_oid" =~ ^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$ ]]; then
		printf 'merge-comments: invalid local oid: %s\n' "$local_oid" >&2
		return 3
	fi
	if [[ "$local_oid" =~ ^0+$ ]]; then
		return 0
	fi
	local base
	if ! base="$(git merge-base origin/main "$local_oid" </dev/null)"; then
		printf 'merge-comments: git merge-base failed for %s\n' "$local_oid" >&2
		return 3
	fi
	local check_rc=0
	bash "$check_script" "$base" "$local_oid" </dev/null || check_rc="$?"
	if ((check_rc == 1)); then
		return 1
	elif ((check_rc != 0)); then
		return 3
	fi
	return 0
}

main() {
	if ! git rev-parse --verify origin/main >/dev/null 2>&1; then
		printf 'merge-comments: origin/main not found, skipping\n' >&2
		return 0
	fi
	local script_dir check_script line status=0 ref_count=0 line_rc
	script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	check_script="${MERGE_COMMENTS_CHECK:-$script_dir/../ci/check-merge-comments.sh}"
	while IFS= read -r line || [[ -n "$line" ]]; do
		((ref_count += 1))
		if ((ref_count > max_refs)); then
			printf 'merge-comments: pre-push ref count exceeds %d\n' "$max_refs" >&2
			return 3
		fi
		line_rc=0
		check_ref_line "$check_script" "$line" || line_rc="$?"
		if ((line_rc == 1)); then
			status=1
		elif ((line_rc != 0)); then
			return 3
		fi
	done
	return "$status"
}

main "$@"
