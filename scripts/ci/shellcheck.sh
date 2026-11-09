#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly max_shell_files=4096

# usage prints command-line options and syntax to standard error.
usage() {
	printf 'usage: %s [--root PATH] [--list] [-- PATH...]\n' "${0##*/}" >&2
}

# is_shell_file checks whether a relative repository path is an eligible shell script.
is_shell_file() {
	local root="$1"
	local path="$2"
	case "$path" in
		.config/hiss/testdata/*)
			return 1
		;;
		*.sh | *.bash | *.ksh)
			return 0
		;;
	esac
	LC_ALL=C grep -Iq -m 1 '' "$root/$path" || return 1
	local prefix=''
	# read fails at end of file before 256 bytes or a newline; the partial prefix it stored still counts.
	IFS= read -r -n 256 prefix <"$root/$path" || [[ -n "$prefix" ]] || return 1
	[[ "$prefix" =~ ^\#\!.*(^|[[:space:]/])(sh|bash|dash|ksh|zsh)([[:space:]]|$) ]]
}

# collect_files discovers all tracked and untracked shell scripts across the repository.
collect_files() {
	local root="$1"
	local path
	while IFS= read -r -d '' path; do
		[[ -f "$root/$path" || -L "$root/$path" ]] || continue
		is_shell_file "$root" "$path" || continue
		if [[ -L "$root/$path" ]]; then
			printf 'ShellCheck refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		files+=("$path")
		if (( ${#files[@]} > max_shell_files )); then
			printf 'ShellCheck file bound exceeded: %d > %d\n' \
				"${#files[@]}" "$max_shell_files" >&2
			return 1
		fi
	done < <(git -C "$root" ls-files -z --cached --others --exclude-standard)
}

# check_suppression_file verifies that every disable directive carries an inline WHY comment.
check_suppression_file() {
	local root="$1"
	local path="$2"
	local line=''
	local line_number=0
	while IFS= read -r line || [[ -n "$line" ]]; do
		((line_number += 1))
		if [[ "$line" =~ ^[[:space:]]*#[[:space:]]*shellcheck[[:space:]]+disable= ]] &&
			[[ ! "$line" =~ ^[[:space:]]*#[[:space:]]*shellcheck[[:space:]]+disable=[^#]+#[[:space:]]*[^#[:space:]] ]]; then
			printf 'ShellCheck suppression needs an inline WHY: %s:%d\n' \
				"$path" "$line_number" >&2
			return 1
		fi
	done <"$root/$path"
}

# check_suppression_explanations validates suppressions in selected scripts and workflow files.
check_suppression_explanations() {
	local root="$1"
	shift
	local path
	local check_idx=0
	for path in "$@"; do
		((check_idx += 1))
		if (( check_idx > max_shell_files )); then
			break
		fi
		check_suppression_file "$root" "$path"
	done
	local workflow_count=0
	while IFS= read -r -d '' path; do
		[[ -f "$root/$path" && ! -L "$root/$path" ]] || continue
		((workflow_count += 1))
		if (( workflow_count > max_shell_files )); then
			printf 'ShellCheck workflow bound exceeded: %d > %d\n' \
				"$workflow_count" "$max_shell_files" >&2
			return 1
		fi
		check_suppression_file "$root" "$path"
	done < <(
		git -C "$root" ls-files -z --cached --others --exclude-standard -- \
			.github/workflows .gitea/workflows
	)
}

# locate_shellcheck resolves the ShellCheck executable path from override or PATH.
locate_shellcheck() {
	local shellcheck_bin="${SHELLCHECK_BIN:-}"
	if [[ -z "$shellcheck_bin" ]] && command -v shellcheck >/dev/null; then
		shellcheck_bin="$(command -v shellcheck)"
	fi
	printf '%s\n' "$shellcheck_bin"
}

# verify_shellcheck asserts the ShellCheck binary is executable and matches the pinned version.
verify_shellcheck() {
	local root="$1"
	local shellcheck_bin="$2"
	# shellcheck source=/dev/null
	. "$root/tools/tool-versions.env"
	if [[ -z "$shellcheck_bin" || ! -x "$shellcheck_bin" ]]; then
		printf 'ShellCheck %s is required; executable is unavailable\n' \
			"$SHELLCHECK_VERSION" >&2
		return 1
	fi
	local actual_version
	actual_version="$("$shellcheck_bin" --version | awk '$1 == "version:" { print $2; exit }')"
	if [[ "$actual_version" != "$SHELLCHECK_VERSION" ]]; then
		printf 'ShellCheck version is %s, want %s\n' \
			"${actual_version:-unknown}" "$SHELLCHECK_VERSION" >&2
		return 1
	fi
}

# collect_path_mode_files categorises supplied path arguments into shell scripts and workflows.
collect_path_mode_files() {
	local root="$1"
	shift
	local path_idx=0
	local path
	for path in "$@"; do
		((path_idx += 1))
		if (( path_idx > max_shell_files )); then
			break
		fi
		if [[ "$path" == *$'\n'* ]]; then
			printf 'ShellCheck path contains newline: %s\n' "$path" >&2
			return 1
		fi
		if [[ ! -e "$root/$path" && ! -L "$root/$path" ]]; then
			continue
		fi
		if [[ -L "$root/$path" ]]; then
			printf 'ShellCheck refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		[[ -f "$root/$path" ]] || continue
		case "$path" in
			.github/workflows/* | .gitea/workflows/*)
				workflow_files+=("$path")
				;;
			*)
				if is_shell_file "$root" "$path"; then
					selected_files+=("$path")
				fi
				;;
		esac
	done
}

# run_repo_mode executes repository-wide ShellCheck analysis over all tracked scripts.
run_repo_mode() {
	local root="$1"
	local shellcheck_bin="$2"
	local -a files=()
	collect_files "$root"
	if (( ${#files[@]} == 0 )); then
		printf 'ShellCheck selected no repository shell scripts\n' >&2
		return 1
	fi
	check_suppression_explanations "$root" "${files[@]}"
	(
		cd "$root"
		"$shellcheck_bin" \
			--external-sources \
			--source-path="$root" \
			--source-path=SCRIPTDIR \
			--severity=style \
			"${files[@]}"
	)
	printf 'ShellCheck: %d repository shell scripts passed\n' "${#files[@]}"
}

# run_path_mode filters given paths, runs ShellCheck when needed, or lists selected files.
run_path_mode() {
	local root="$1"
	local list_only="$2"
	shift 2
	if (( $# > max_shell_files )); then
		printf 'ShellCheck file bound exceeded: %d > %d\n' \
			"$#" "$max_shell_files" >&2
		return 1
	fi
	local -a selected_files=()
	local -a workflow_files=()
	collect_path_mode_files "$root" "$@" || return 1
	if (( list_only )); then
		local out_idx=0
		local path
		for path in "${selected_files[@]}"; do
			((out_idx += 1))
			if (( out_idx > max_shell_files )); then
				break
			fi
			printf '%s\n' "$path"
		done
		return 0
	fi
	local check_idx=0
	local path
	for path in "${workflow_files[@]}"; do
		((check_idx += 1))
		if (( check_idx > max_shell_files )); then
			break
		fi
		check_suppression_file "$root" "$path"
	done
	if (( ${#selected_files[@]} == 0 )); then
		printf 'ShellCheck: no shell scripts among %d paths\n' "$#"
		return 0
	fi
	local shellcheck_bin
	shellcheck_bin="$(locate_shellcheck)"
	verify_shellcheck "$root" "$shellcheck_bin"
	check_idx=0
	for path in "${selected_files[@]}"; do
		((check_idx += 1))
		if (( check_idx > max_shell_files )); then
			break
		fi
		check_suppression_file "$root" "$path"
	done
	(
		cd "$root"
		"$shellcheck_bin" \
			--external-sources \
			--source-path="$root" \
			--source-path=SCRIPTDIR \
			--severity=style \
			"${selected_files[@]}"
	)
	printf 'ShellCheck: %d changed shell scripts passed\n' "${#selected_files[@]}"
}

# resolve_root prints the absolute repository root from --root or the script location.
resolve_root() {
	if [[ -n "$1" ]]; then
		(cd "$1" && pwd)
	else
		(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
	fi
}

# main orchestrates option parsing and dispatches to repository or path analysis mode.
main() {
	local is_path_mode=0
	local list_only=0
	local custom_root=""
	local -a path_args=()
	local opt_count=0

	while [[ $# -gt 0 ]]; do
		((opt_count += 1))
		if (( opt_count > 3 )); then
			usage
			return 2
		fi
		case "$1" in
			--)
				is_path_mode=1
				shift
				path_args=("$@")
				break
				;;
			--root)
				if [[ $# -lt 2 ]]; then
					usage
					return 2
				fi
				custom_root="$2"
				shift 2
				;;
			--list)
				list_only=1
				shift
				;;
			*)
				usage
				return 2
				;;
		esac
	done

	if (( ! is_path_mode && list_only )); then
		usage
		return 2
	fi

	local root
	root="$(resolve_root "$custom_root")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null

	if (( is_path_mode )); then
		run_path_mode "$root" "$list_only" "${path_args[@]}"
	else
		local shellcheck_bin
		shellcheck_bin="$(locate_shellcheck)"
		verify_shellcheck "$root" "$shellcheck_bin"
		run_repo_mode "$root" "$shellcheck_bin"
	fi
}

main "$@"
