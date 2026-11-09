#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# pre-commit / pre-push: run the pinned ShellCheck gate on the staged or pushed shell scripts and workflow files.

set -euo pipefail

readonly max_paths=4096

# shellcheck source=scripts/hooks/lib.sh
. "$(dirname "$0")/lib.sh"

root="$(git rev-parse --show-toplevel)"

# shellcheck source=/dev/null
. "$root/tools/tool-versions.env"

# normalize_paths converts backslashes to forward slashes across path arguments.
normalize_paths() {
	local arg
	local path_count=0
	for arg in "$@"; do
		((path_count += 1))
		if (( path_count > max_paths )); then
			break
		fi
		# Windows Git passes backslash-separated paths.
		paths+=("${arg//\\//}")
	done
}

# check_missing_content verifies that deleted paths are not still present in index or HEAD.
check_missing_content() {
	local mode="$1"
	shift
	local path
	local path_count=0
	for path in "$@"; do
		((path_count += 1))
		if (( path_count > max_paths )); then
			break
		fi
		if [[ ! -e "$root/$path" && ! -L "$root/$path" ]]; then
			local lookup_out=''
			local lookup_status=0
			if [[ "$mode" == "--staged" ]]; then
				lookup_out="$(git -C "$root" --literal-pathspecs ls-files -s -- "$path")" || lookup_status=$?
			elif [[ "$mode" == "--pushed" ]]; then
				lookup_out="$(git -C "$root" --literal-pathspecs ls-tree HEAD -- "$path")" || lookup_status=$?
			fi
			if (( lookup_status != 0 )); then
				fail "$path: git lookup failed (exit $lookup_status)"
			fi
			if [[ -n "$lookup_out" ]]; then
				if [[ "$mode" == "--staged" ]]; then
					fail "$path: staged but removed from the worktree"
				elif [[ "$mode" == "--pushed" ]]; then
					fail "$path: committed in HEAD but removed from the worktree"
				fi
			fi
		fi
	done
}

# check_divergence verifies that worktree scripts match index (staged) or HEAD (pushed).
check_divergence() {
	local mode="$1"
	shift
	local diff_status=0
	if [[ "$mode" == "--staged" ]]; then
		git -C "$root" diff --quiet -- "$@" || diff_status=$?
		if (( diff_status == 1 )); then
			fail "ShellCheck would read unstaged content (stage or drop the changes)"
		elif (( diff_status != 0 )); then
			fail "git diff failed with exit code $diff_status"
		fi
	elif [[ "$mode" == "--pushed" ]]; then
		git -C "$root" diff --quiet HEAD -- "$@" || diff_status=$?
		if (( diff_status == 1 )); then
			fail "ShellCheck would read uncommitted changes in a pushed script (commit or stash them)"
		elif (( diff_status != 0 )); then
			fail "git diff failed with exit code $diff_status"
		fi
	fi
}

# run_without_shellcheck checks workflow suppressions and fails if shell scripts need ShellCheck.
run_without_shellcheck() {
	local mode="$1"
	shift
	local -a workflow_items=()
	local -a script_items=()
	local split_count=0
	local item
	for item in "$@"; do
		((split_count += 1))
		if (( split_count > max_paths )); then
			break
		fi
		case "$item" in
			.github/workflows/* | .gitea/workflows/*)
				workflow_items+=("$item")
				;;
			*)
				script_items+=("$item")
				;;
		esac
	done
	if (( ${#workflow_items[@]} > 0 )); then
		check_divergence "$mode" "${workflow_items[@]}"
		bash "$root/scripts/ci/shellcheck.sh" --root "$root" -- "${workflow_items[@]}" ||
			fail "ShellCheck reported findings"
	fi
	if (( ${#script_items[@]} > 0 )); then
		fail "ShellCheck ${SHELLCHECK_VERSION} is not on PATH and ${#script_items[@]} changed shell script(s) need it — install with: bash scripts/ci/install-shellcheck.sh <dir>"
	fi
	note "${#workflow_items[@]} changed workflow file(s) passed"
	exit 0
}

# main coordinates argument validation, selection filtering, and ShellCheck invocation.
main() {
	if [[ $# -lt 1 ]]; then
		fail "usage: ${0##*/} --staged|--pushed [PATH...]"
	fi
	local mode="$1"
	shift
	case "$mode" in
		--staged | --pushed)
			;;
		*)
			fail "usage: ${0##*/} --staged|--pushed [PATH...]"
			;;
	esac

	if (( $# > max_paths )); then
		note "changed path count exceeds bound ($# > $max_paths); checking all repository shell scripts"
		bash "$root/scripts/ci/shellcheck.sh" --root "$root" ||
			fail "ShellCheck reported findings"
		exit 0
	fi

	local -a paths=()
	normalize_paths "$@"

	check_missing_content "$mode" ${paths[@]+"${paths[@]}"}

	local list_output
	list_output="$(bash "$root/scripts/ci/shellcheck.sh" --root "$root" --list -- ${paths[@]+"${paths[@]}"})"

	local -a selected=()
	if [[ -n "$list_output" ]]; then
		local sel_count=0
		local item
		while IFS= read -r item || [[ -n "$item" ]]; do
			((sel_count += 1))
			if (( sel_count > max_paths )); then
				break
			fi
			[[ -n "$item" ]] && selected+=("$item")
		done <<<"$list_output"
	fi

	((${#selected[@]} > 0)) || skip "no changed shell scripts or workflows"

	if [[ -z "${SHELLCHECK_BIN:-}" ]] && ! command -v shellcheck >/dev/null 2>&1; then
		run_without_shellcheck "$mode" "${selected[@]}"
	fi

	check_divergence "$mode" "${selected[@]}"

	bash "$root/scripts/ci/shellcheck.sh" --root "$root" -- "${selected[@]}" ||
		fail "ShellCheck reported findings"

	note "${#selected[@]} changed shell script or workflow file(s) passed"
}

main "$@"
