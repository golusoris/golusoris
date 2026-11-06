#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Refuses a minor or major release without depth evidence (e2e, upgrade, dogfood) for the range it covers.
#   check <tag> [<end-ref>]  a minor or major tag (vX.Y.Z root, core/vX.Y.Z core) needs .config/release-evidence/<tag>/
#                            e2e.tsv, upgrade.log and dogfood.log; patch tags pass; <end-ref> is required before the tag exists
#   pending                  runs check on HEAD for every component of .release-please-manifest.json whose tag is not yet created

set -euo pipefail

readonly max_commits=5000
readonly max_rows=5000
readonly max_tags=1000

root=''
errors=()

usage() {
	printf 'usage: %s [--root DIR] check <tag> [<end-ref>] | [--root DIR] pending\n' \
		"${0##*/}" >&2
}

rgit() {
	git -C "$root" "$@"
}

fail() {
	errors+=("$1")
}

# find_base prints the highest-version tag of the tag's component that is an ancestor of the end commit.
# Returns 1 when there is none and 2 when more than max_tags tags were scanned without a match.
find_base() {
	local tag="$1" end="$2" prefix="$3" candidate pattern scanned=0
	if [[ -z "$prefix" ]]; then
		pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
	else
		pattern='^core/v[0-9]+\.[0-9]+\.[0-9]+$'
	fi
	while IFS= read -r candidate; do
		if [[ "$candidate" == "$tag" ]]; then
			continue
		fi
		scanned=$((scanned + 1))
		if ((scanned > max_tags)); then
			return 2
		fi
		if rgit merge-base --is-ancestor "refs/tags/$candidate^{commit}" "$end"; then
			printf '%s\n' "$candidate"
			return 0
		fi
	done < <(rgit tag --list "${prefix}v*.*.*" | re="$pattern" LC_ALL=C awk '$0 ~ ENVIRON["re"]' | LC_ALL=C sort -V -r)
	return 1
}

is_user_visible() {
	local subject="$1" body="$2"
	local release_re='^chore(\(.+\))?!?:[[:space:]]*release'
	local visible_re='^(feat|fix|perf|refactor|revert)(\(.+\))?!?:'
	if [[ "$subject" =~ $release_re ]]; then
		return 1
	fi
	[[ "$subject" =~ $visible_re ]] || [[ "$body" == *'BREAKING CHANGE'* ]]
}

# list_required fills required_shas and required_subjects with the user-visible commits of base..end.
# Returns 2 when the range exceeds max_commits.
list_required() {
	local base="$1" end="$2" prefix="$3" rec sha subject body count=0
	local -a args=(log --first-parent "--max-count=$((max_commits + 1))"
		'--format=%H%x1f%s%x1f%b%x1e' "$base..$end")
	if [[ -n "$prefix" ]]; then
		args+=(-- core/)
	fi
	required_shas=()
	required_subjects=()
	while IFS= read -r -d $'\x1e' rec; do
		count=$((count + 1))
		if ((count > max_commits)); then
			return 2
		fi
		rec="${rec#$'\n'}"
		sha="${rec%%$'\x1f'*}"
		rec="${rec#*$'\x1f'}"
		subject="${rec%%$'\x1f'*}"
		body="${rec#*$'\x1f'}"
		if is_user_visible "$subject" "$body"; then
			required_shas+=("$sha")
			required_subjects+=("$subject")
		fi
	done < <(rgit "${args[@]}")
}

# load_rows reads e2e.tsv into row_set (sha prefixes) and reports FAIL, empty-reason SKIP and malformed rows.
load_rows() {
	local file="$1" label="$2" sha status reason count=0
	local sha_re='^[0-9a-f]{7,40}$'
	declare -gA row_set=()
	while IFS=$'\x1f' read -r sha _ _ status reason; do
		count=$((count + 1))
		if ((count > max_rows)); then
			fail "$label has more than $max_rows rows"
			return 0
		fi
		if [[ ! "$sha" =~ $sha_re ]]; then
			fail "$label row $count: '$sha' is not a 7 to 40 digit lowercase hex sha"
			continue
		fi
		row_set["$sha"]=1
		case "$status" in
			PASS) ;;
			SKIP)
				if [[ -z "${reason//[[:space:]]/}" ]]; then
					fail "$label row $count ($sha): SKIP without a reason"
				fi
				;;
			FAIL) fail "$label row $count ($sha): FAIL" ;;
			*) fail "$label row $count ($sha): status '$status' is not PASS, SKIP or FAIL" ;;
		esac
	done < <(LC_ALL=C awk -F'\t' '
		/^[[:space:]]*#/ || NF == 0 { next }
		{ printf "%s\037%s\037%s\037%s\037%s\n", $1, $2, $3, $4, $5 }
	' "$file" | tr -d '\r')
}

has_row() {
	local full="$1" length
	for ((length = 7; length <= 40; length++)); do
		if [[ -n "${row_set[${full:0:length}]:-}" ]]; then
			return 0
		fi
	done
	return 1
}

header_base() {
	LC_ALL=C awk '
		/^# base:/ {
			sub(/^# base:[[:space:]]*/, "")
			sub(/[[:space:]]+$/, "")
			print
			exit
		}
	' "$1" | tr -d '\r'
}

# A marker line may carry one leading `date -Is` stamp, as the proof scripts log it.
log_has_line() {
	tr -d '\r' <"$1" |
		sed -E 's/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([+-][0-9]{2}:[0-9]{2}|Z) //' |
		grep -Fxq -- "$2"
}

check_evidence() {
	local base="$1" dir="$2" relative="$3"
	local declared index
	if [[ ! -d "$dir" ]]; then
		fail "evidence directory $relative is missing"
		return 0
	fi
	local file
	for file in e2e.tsv upgrade.log dogfood.log; do
		if [[ ! -f "$dir/$file" ]]; then
			fail "$relative/$file is missing"
		fi
	done
	if [[ -f "$dir/e2e.tsv" ]]; then
		declared="$(header_base "$dir/e2e.tsv")"
		if [[ -z "$declared" ]]; then
			fail "$relative/e2e.tsv has no '# base:' header (computed base is $base)"
		elif [[ "$declared" != "$base" ]]; then
			fail "$relative/e2e.tsv names '# base: $declared' but the release range starts after $base"
		fi
		load_rows "$dir/e2e.tsv" "$relative/e2e.tsv"
		for index in "${!required_shas[@]}"; do
			if ! has_row "${required_shas[$index]}"; then
				fail "user-visible commit ${required_shas[$index]:0:7} (${required_subjects[$index]}) has no row in $relative/e2e.tsv"
			fi
		done
	fi
	if [[ -f "$dir/upgrade.log" ]]; then
		if ! log_has_line "$dir/upgrade.log" "from: $base"; then
			fail "$relative/upgrade.log lacks the line 'from: $base'"
		fi
		if ! log_has_line "$dir/upgrade.log" 'result: PASS'; then
			fail "$relative/upgrade.log lacks the line 'result: PASS'"
		fi
	fi
	if [[ -f "$dir/dogfood.log" ]] && ! log_has_line "$dir/dogfood.log" 'result: PASS'; then
		fail "$relative/dogfood.log lacks the line 'result: PASS'"
	fi
}

report_errors() {
	local message
	for message in "${errors[@]}"; do
		printf 'release-depth-evidence: %s\n' "$message" >&2
	done
	return 1
}

# Sets the caller's end_name and end (bash dynamic scope); fails when neither resolves.
resolve_end() {
	local tag="$1" given_end="$2"
	if [[ -n "$given_end" ]]; then
		end_name="$given_end"
	elif rgit rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
		end_name="refs/tags/$tag"
	else
		fail "tag $tag does not exist yet; name the end-ref to check"
		return 1
	fi
	if ! end="$(rgit rev-parse -q --verify "$end_name^{commit}")"; then
		fail "end-ref $end_name for $tag does not resolve to a commit"
		return 1
	fi
}

check_release() {
	local tag="$1" given_end="${2:-}"
	local tag_re='^(core/)?v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
	errors=()
	if [[ ! "$tag" =~ $tag_re ]]; then
		printf 'not a release tag (vX.Y.Z or core/vX.Y.Z): %s\n' "$tag" >&2
		return 2
	fi
	local prefix="${BASH_REMATCH[1]}" patch="${BASH_REMATCH[4]}"
	if [[ "$patch" != 0 ]]; then
		printf 'release-depth-evidence: %s is a patch release; no depth evidence required\n' "$tag"
		return 0
	fi
	if ! rgit rev-parse --git-dir >/dev/null 2>&1; then
		fail "$root is not a git repository"
		report_errors
		return 1
	fi
	local end end_name
	if ! resolve_end "$tag" "$given_end"; then
		report_errors
		return 1
	fi
	local base status=0
	base="$(find_base "$tag" "$end" "$prefix")" || status=$?
	case "$status" in
		0) ;;
		2)
			fail "more than $max_tags tags scanned for the base of $tag"
			report_errors
			return 1
			;;
		*)
			fail "no earlier release tag of the same component is an ancestor of $end_name; no base for $tag"
			report_errors
			return 1
			;;
	esac
	status=0
	list_required "$base" "$end" "$prefix" || status=$?
	if ((status != 0)); then
		fail "more than $max_commits commits in $base..$end_name; range over the cap"
		report_errors
		return 1
	fi
	local name="${tag//\//-}"
	local relative=".config/release-evidence/$name"
	check_evidence "$base" "$root/$relative" "$relative"
	if ((${#errors[@]} > 0)); then
		report_errors
		return 1
	fi
	printf 'release-depth-evidence: %s has depth evidence over %s..%s (%d user-visible commits)\n' \
		"$tag" "$base" "$end_name" "${#required_shas[@]}"
}

check_pending() {
	local manifest="$root/.release-please-manifest.json" tags tag status=0
	if [[ ! -f "$manifest" ]]; then
		printf 'no .release-please-manifest.json under %s\n' "$root" >&2
		return 1
	fi
	tags="$(python3 -I -c '
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    manifest = json.load(handle)
for key, prefix in ((".", ""), ("core", "core/")):
    version = manifest.get(key)
    if not isinstance(version, str):
        sys.exit("manifest has no string version for component " + repr(key))
    print(prefix + "v" + version)
' "$manifest")" || {
		printf 'cannot read the component versions of %s\n' "$manifest" >&2
		return 1
	}
	while IFS= read -r tag; do
		if rgit rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
			printf 'release-depth-evidence: %s already released; skipped\n' "$tag"
			continue
		fi
		check_release "$tag" HEAD || status=1
	done <<<"$tags"
	return "$status"
}

main() {
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if [[ "${1:-}" == --root ]]; then
		if (($# < 2)); then
			usage
			return 2
		fi
		root="$(cd "$2" && pwd)"
		shift 2
	fi
	case "${1:-}" in
		check)
			if (($# < 2 || $# > 3)); then
				usage
				return 2
			fi
			check_release "$2" "${3:-}"
			;;
		pending)
			if (($# != 1)); then
				usage
				return 2
			fi
			check_pending
			;;
		*)
			usage
			return 2
			;;
	esac
}

main "$@"
