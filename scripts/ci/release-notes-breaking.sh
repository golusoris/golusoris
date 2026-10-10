#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Ties breaking CHANGELOG entries to their migration guide (#740).
#   check                    the newest CHANGELOG entry, when breaking, needs docs/migrations/v<version>.md
#   notes <tag> [<previous>] prints the release-notes block for <tag>: every breaking entry after the
#                            previous published release (every entry when none is named), each with its
#                            guide; prints nothing when no entry in that range is breaking

set -euo pipefail

usage() {
	printf 'usage: %s [--root DIR] check | [--root DIR] notes <tag> [<previous-published-tag>]\n' \
		"${0##*/}" >&2
}

# changelog_entries prints "V<TAB>version" per release entry, newest first, and
# "B<TAB>version<TAB>text" per bullet of that entry's breaking-changes section.
changelog_entries() {
	LC_ALL=C awk '
		/^## / {
			version = ""
			breaking = 0
			if (match($0, /^## \[?v?[0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+-]*/)) {
				version = substr($0, RSTART, RLENGTH)
				sub(/^## \[?v?/, "", version)
				printf "V\t%s\n", version
			}
			next
		}
		/^### / {
			breaking = ($0 ~ /BREAKING CHANGES/)
			next
		}
		breaking && version != "" && /^\* / {
			text = $0
			sub(/^\* /, "", text)
			printf "B\t%s\t%s\n", version, text
		}
	' "$1"
}

# guide_headings prints the second-level headings of a migration guide, one per line.
guide_headings() {
	LC_ALL=C awk '/^## / { sub(/^## /, ""); print }' "$1"
}

# require_guide fails closed when the breaking entry of version has no guide of exactly that name.
require_guide() {
	local root="$1" version="$2"
	if [[ ! -f "$root/docs/migrations/v$version.md" ]]; then
		printf 'CHANGELOG entry %s has breaking changes but docs/migrations/v%s.md is missing\n' \
			"$version" "$version" >&2
		return 1
	fi
}

check_newest() {
	local root="$1" entries newest
	entries="$(changelog_entries "$root/CHANGELOG.md")"
	newest="$(awk -F'\t' '$1 == "V" { print $2; exit }' <<<"$entries")"
	if [[ -z "$newest" ]]; then
		printf 'CHANGELOG.md has no release entry\n' >&2
		return 1
	fi
	if awk -F'\t' -v v="$newest" '$1 == "B" && $2 == v { found = 1 } END { exit !found }' <<<"$entries"; then
		require_guide "$root" "$newest"
	fi
}

# versions_in_range prints the entries from the tag's own down to, not including, the previous one.
versions_in_range() {
	local entries="$1" version="$2" previous="$3"
	awk -F'\t' -v from="$version" -v until="$previous" '
		$1 != "V" { next }
		$2 == from { inside = 1 }
		inside && until != "" && $2 == until { closed = 1; exit }
		inside { print $2 }
		END {
			if (!inside) { exit 3 }
			if (until != "" && !closed) { exit 4 }
		}
	' <<<"$entries"
}

print_notes() {
	local root="$1" tag="$2" previous_tag="$3"
	local repository="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY names the repository the guide links point at}"
	local version="${tag#v}" previous="${previous_tag#v}"
	local entries range status=0
	entries="$(changelog_entries "$root/CHANGELOG.md")"
	range="$(versions_in_range "$entries" "$version" "$previous")" || status=$?
	case "$status" in
		0) ;;
		3)
			printf 'CHANGELOG.md has no entry for %s\n' "$version" >&2
			return 1
			;;
		*)
			printf 'CHANGELOG.md has no entry for the previous release %s below %s\n' "$previous" "$version" >&2
			return 1
			;;
	esac

	local -a breaking=()
	local candidate
	while IFS= read -r candidate; do
		if awk -F'\t' -v v="$candidate" '$1 == "B" && $2 == v { found = 1 } END { exit !found }' <<<"$entries"; then
			require_guide "$root" "$candidate" || return 1
			breaking+=("$candidate")
		fi
	done <<<"$range"
	if ((${#breaking[@]} == 0)); then
		return 0
	fi

	printf '## Breaking changes and migration\n\n'
	local -a earlier=()
	for candidate in "${breaking[@]}"; do
		if [[ "$candidate" != "$version" ]]; then
			earlier+=("$candidate")
		fi
	done
	if ((${#earlier[@]} == 0)); then
		printf 'This release contains breaking changes.\n'
	else
		local joined
		joined="$(printf '%s, ' "${earlier[@]}")"
		printf '%s is the first published release that carries the breaking changes of %s.\n' \
			"$tag" "${joined%, }"
	fi
	for candidate in "${breaking[@]}"; do
		printf '\n**%s**\n\n' "$candidate"
		awk -F'\t' -v v="$candidate" '$1 == "B" && $2 == v { printf "- %s\n", $3 }' <<<"$entries"
		printf '\nWhat changes for callers:\n\n'
		guide_headings "$root/docs/migrations/v$candidate.md" | sed 's/^/- /'
		printf '\nMigration guide: https://github.com/%s/blob/%s/docs/migrations/v%s.md\n' \
			"$repository" "$tag" "$candidate"
	done
}

main() {
	local root
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if [[ "${1:-}" == --root ]]; then
		if (($# < 2)); then
			usage
			return 2
		fi
		root="$(cd "$2" && pwd)"
		shift 2
	fi
	if [[ ! -f "$root/CHANGELOG.md" ]]; then
		printf 'no CHANGELOG.md under %s\n' "$root" >&2
		return 1
	fi
	case "${1:-}" in
		check)
			if (($# != 1)); then
				usage
				return 2
			fi
			check_newest "$root"
			;;
		notes)
			if (($# < 2 || $# > 3)); then
				usage
				return 2
			fi
			if [[ ! "$2" =~ ^v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
				printf 'not a release tag: %s\n' "$2" >&2
				return 2
			fi
			print_notes "$root" "$2" "${3:-}"
			;;
		*)
			usage
			return 2
			;;
	esac
}

main "$@"
