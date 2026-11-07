#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Refuses a minor or major release without depth evidence (e2e, upgrade, dogfood) for the range it covers.
#   check <tag> [<end-ref>]  a minor or major tag (vX.Y.Z root, core/vX.Y.Z core) needs .config/release-evidence/<tag>/
#                            e2e.tsv, upgrade.log and dogfood.log; patch tags pass; <end-ref> is required before the tag exists
#   pending                  runs check on HEAD for every component of .release-please-manifest.json whose tag is not yet created

set -euo pipefail

# Runs on bash 3.2 (macOS), Git Bash (Windows) and Linux: no associative arrays, no sort -V,
# and every array expansion guards against an empty array under set -u.

readonly max_commits=5000
readonly max_rows=5000
readonly max_tags=1000

root=''
errors=()
work="$(mktemp -d "${TMPDIR:-/tmp}/release-depth-evidence.XXXXXX")"
readonly work
trap 'rm -rf "$work"' EXIT

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
	done < <(rgit tag --list "${prefix}v*.*.*" | re="$pattern" LC_ALL=C awk '
		$0 ~ ENVIRON["re"] {
			version = $0
			sub(/^(core\/)?v/, "", version)
			split(version, part, ".")
			print part[1], part[2], part[3], $0
		}
	' | LC_ALL=C sort -k1,1nr -k2,2nr -k3,3nr | LC_ALL=C awk '{ print $4 }')
	return 1
}

# is_visible_message matches a Conventional Commit header that release-please lists: a
# changelog-visible type (release-please-config.json changelog-sections) or `!` on any type.
is_visible_message() {
	local visible_re='^(feat|fix|perf|refactor|revert|deps)(\([^)]+\))?!?:'
	local breaking_re='^[a-z]+(\([^)]+\))?!:'
	[[ "$1" =~ $visible_re ]] || [[ "$1" =~ $breaking_re ]]
}

# is_user_visible also reads the body for a breaking footer in either spelling. Conventional
# Commit lines quoted in a body (Renovate release notes) stay out: release-please does not list them.
is_user_visible() {
	local subject="$1" body="$2"
	local release_re='^chore(\([^)]+\))?:[[:space:]]*release'
	local footer_re=$'\n''BREAKING[ -]CHANGE:'
	if [[ $'\n'"${body//$'\r'/}" =~ $footer_re ]]; then
		return 0
	fi
	# Only a non-breaking release commit is skipped; `chore!:` stays visible through is_visible_message.
	if [[ "$subject" =~ $release_re ]]; then
		return 1
	fi
	is_visible_message "$subject"
}

# list_required fills required_shas and required_subjects with the user-visible commits of base..end.
# Returns 2 when the range exceeds max_commits and 3 when git log fails.
list_required() {
	local base="$1" end="$2" prefix="$3" rec sha subject body count=0 history
	local -a args=(log --first-parent "--max-count=$((max_commits + 1))"
		'--format=%H%x1f%s%x1f%b%x1e' "$base..$end")
	if [[ -n "$prefix" ]]; then
		args+=(-- core/)
	fi
	required_shas=()
	required_subjects=()
	if ! history="$(rgit "${args[@]}")"; then
		return 3
	fi
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
	done <<<"$history"
}

# rows_program is the awk pass of check_rows; REQUIRED holds one "<sha>\t<subject>" line per required commit.
rows_program="$(cat <<'AWK'
		# The first file holds the required commits, one "sha<TAB>subject" per line.
		FILENAME == ARGV[1] {
			need[++count] = $0
			next
		}
		{ sub(/\r$/, "") }
		/^[[:space:]]*#/ || NF == 0 { next }
		{
			rows++
			if (rows > ENVIRON["MAX_ROWS"] + 0) {
				print ENVIRON["LABEL"] " has more than " ENVIRON["MAX_ROWS"] " rows"
				over = 1
				exit
			}
			sha = $1
			if (sha !~ /^[0-9a-f]+$/ || length(sha) < 7 || length(sha) > 40) {
				printf "%s row %d: \047%s\047 is not a 7 to 40 digit lowercase hex sha\n", ENVIRON["LABEL"], rows, sha
				next
			}
			status[sha] = $4
			reason = $5
			gsub(/[[:space:]]/, "", reason)
			if ($4 == "SKIP" && reason == "") {
				printf "%s row %d (%s): SKIP without a reason\n", ENVIRON["LABEL"], rows, sha
			} else if ($4 == "FAIL") {
				printf "%s row %d (%s): FAIL\n", ENVIRON["LABEL"], rows, sha
			} else if ($4 != "PASS" && $4 != "SKIP") {
				printf "%s row %d (%s): status \047%s\047 is not PASS, SKIP or FAIL\n", ENVIRON["LABEL"], rows, sha, $4
			}
		}
		END {
			if (over) {
				exit
			}
			for (i = 1; i <= count; i++) {
				if (need[i] == "") {
					continue
				}
				tab = index(need[i], "\t")
				full = substr(need[i], 1, tab - 1)
				subject = substr(need[i], tab + 1)
				found = ""
				for (length_ = 7; length_ <= 40 && found == ""; length_++) {
					prefix = substr(full, 1, length_)
					if (prefix in status) {
						found = status[prefix]
					}
				}
				if (found == "") {
					printf "user-visible commit %s (%s) has no row in %s\n", substr(full, 1, 7), subject, ENVIRON["LABEL"]
				} else if (found != "PASS") {
					printf "user-visible commit %s (%s) has status %s in %s; it needs PASS\n", substr(full, 1, 7), subject, found, ENVIRON["LABEL"]
				}
			}
		}
AWK
)"
readonly rows_program

# check_rows reads e2e.tsv in one pass: malformed, FAIL and empty-reason SKIP rows, then every
# required commit whose row (matched by sha prefix) is missing or not PASS.
check_rows() {
	local file="$1" label="$2" index output message
	# A file, not the environment: one environment string is capped (128 KiB on Linux, 32 KiB on Windows).
	: >"$work/required"
	if ((${#required_shas[@]} > 0)); then
		for index in "${!required_shas[@]}"; do
			printf '%s\t%s\n' "${required_shas[$index]}" "${required_subjects[$index]}" >>"$work/required"
		done
	fi
	if ! output="$(LABEL="$label" MAX_ROWS="$max_rows" LC_ALL=C awk -F'\t' "$rows_program" "$work/required" "$file")"; then
		fail "the row check of $label could not run; no row was checked"
		return 0
	fi
	if [[ -n "$output" ]]; then
		while IFS= read -r message; do
			fail "$message"
		done <<<"$output"
	fi
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

# log_lines prints the log with CR and one leading `date -Is` stamp per line removed, as the
# proof scripts log markers. It reads the whole file, so no reader exits early on a long log.
log_lines() {
	LC_ALL=C awk '
		{
			sub(/\r$/, "")
			sub(/^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]([+-][0-9][0-9]:[0-9][0-9]|Z) /, "")
			print
		}
	' "$1"
}

has_line() {
	[[ $'\n'"$1"$'\n' == *$'\n'"$2"$'\n'* ]]
}

# check_result fails unless the log has a `result:` line and every `result:` line is `result: PASS`.
check_result() {
	local lines="$1" relative="$2" line seen=0
	while IFS= read -r line; do
		case "$line" in
			'result: PASS') seen=1 ;;
			result:*)
				fail "$relative has the line '$line'; every result line must be 'result: PASS'"
				return 0
				;;
		esac
	done <<<"$lines"
	if ((seen == 0)); then
		fail "$relative lacks the line 'result: PASS'"
	fi
}

check_evidence() {
	local base="$1" dir="$2" relative="$3" tag="$4"
	local declared lines
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
		check_rows "$dir/e2e.tsv" "$relative/e2e.tsv"
	fi
	if [[ -f "$dir/upgrade.log" ]]; then
		lines="$(log_lines "$dir/upgrade.log")"
		if ! has_line "$lines" "from: $base"; then
			fail "$relative/upgrade.log lacks the line 'from: $base'"
		fi
		if ! has_line "$lines" "to: $tag"; then
			fail "$relative/upgrade.log lacks the line 'to: $tag'"
		fi
		check_result "$lines" "$relative/upgrade.log"
	fi
	if [[ -f "$dir/dogfood.log" ]]; then
		check_dogfood "$dir/dogfood.log" "$relative/dogfood.log" "$tag"
	fi
}

# check_dogfood requires the golusoris-mcp initialize reply to name the version of the binary tested:
# the release version, or for a core tag the root version in the manifest at the end of the range,
# since the binary carries the root version. $end comes from check_release.
check_dogfood() {
	local file="$1" relative="$2" tag="$3" lines line expected found=0 tags
	expected="${tag##*v}"
	if [[ "$tag" == core/* ]]; then
		if ! rgit show "$end:.release-please-manifest.json" >"$work/manifest" 2>/dev/null ||
			! tags="$(manifest_tags "$work/manifest")"; then
			fail "$relative: no root version in .release-please-manifest.json at $end_name for the golusoris-mcp check"
			return 0
		fi
		expected="${tags%%$'\n'*}"
		expected="${expected#v}"
	fi
	lines="$(log_lines "$file")"
	while IFS= read -r line; do
		if [[ "$line" == 'initialize: '*'"version": "'"$expected"'"'* ]]; then
			found=1
		fi
	done <<<"$lines"
	if ((found == 0)); then
		fail "$relative lacks an 'initialize:' line with \"version\": \"$expected\""
	fi
	check_result "$lines" "$relative"
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

# load_range lists the user-visible commits of $1..$2 and records why when it cannot.
load_range() {
	local status=0
	list_required "$1" "$2" "$3" || status=$?
	case "$status" in
		0) return 0 ;;
		2) fail "more than $max_commits commits in $1..$4; range over the cap" ;;
		*) fail "git log failed for $1..$4; the user-visible commits are unknown" ;;
	esac
	return 1
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
	if ! load_range "$base" "$end" "$prefix" "$end_name"; then
		report_errors
		return 1
	fi
	local name="${tag//\//-}"
	local relative=".config/release-evidence/$name"
	check_evidence "$base" "$root/$relative" "$relative" "$tag"
	if ((${#errors[@]} > 0)); then
		report_errors
		return 1
	fi
	printf 'release-depth-evidence: %s has depth evidence over %s..%s (%d user-visible commits)\n' \
		"$tag" "$base" "$end_name" "${#required_shas[@]}"
}

# manifest_tags prints the root tag, then the core tag, named by the release-please manifest $1.
manifest_tags() {
	local tags
	[[ -f "$1" ]] || return 1
	tags="$("${PYTHON:-python3}" -I -c '
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    manifest = json.load(handle)
for key, prefix in ((".", ""), ("core", "core/")):
    version = manifest.get(key)
    if not isinstance(version, str):
        sys.exit("manifest has no string version for component " + repr(key))
    print(prefix + "v" + version)
' "$1")" || return 1
	# Python on Windows ends its lines with CRLF.
	printf '%s\n' "${tags//$'\r'/}"
}

check_pending() {
	local manifest="$root/.release-please-manifest.json" tags tag status=0
	if [[ ! -f "$manifest" ]]; then
		printf 'no .release-please-manifest.json under %s\n' "$root" >&2
		return 1
	fi
	if ! tags="$(manifest_tags "$manifest")"; then
		printf 'cannot read the component versions of %s\n' "$manifest" >&2
		return 1
	fi
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
