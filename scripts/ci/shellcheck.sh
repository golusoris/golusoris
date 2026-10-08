#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly max_shell_files=4096

usage() {
	printf 'usage: %s [--root PATH]\n' "${0##*/}" >&2
}

resolve_root() {
	local root
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if [[ $# -eq 0 ]]; then
		printf '%s\n' "$root"
		return
	fi
	if [[ $# -ne 2 || "$1" != --root ]]; then
		usage
		return 2
	fi
	(cd "$2" && pwd)
}

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

check_suppression_explanations() {
	local root="$1"
	shift
	local path
	for path in "$@"; do
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

main() {
	local root
	root="$(resolve_root "$@")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null

	# shellcheck source=/dev/null
	. "$root/tools/tool-versions.env"
	local shellcheck_bin="${SHELLCHECK_BIN:-}"
	if [[ -z "$shellcheck_bin" ]] && command -v shellcheck >/dev/null; then
		shellcheck_bin="$(command -v shellcheck)"
	fi
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

main "$@"
