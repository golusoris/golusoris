#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly max_workflow_files=512

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

collect_files() {
	local root="$1"
	local path
	while IFS= read -r -d '' path; do
		if [[ -L "$root/$path" ]]; then
			printf 'actionlint refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		[[ -f "$root/$path" ]] || continue
		files+=("$path")
		if (( ${#files[@]} > max_workflow_files )); then
			printf 'actionlint file bound exceeded: %d > %d\n' \
				"${#files[@]}" "$max_workflow_files" >&2
			return 1
		fi
	done < <(git -C "$root" ls-files -z --cached --others --exclude-standard -- \
		'.github/workflows/*.yml' '.github/workflows/*.yaml' \
		'.gitea/workflows/*.yml' '.gitea/workflows/*.yaml')
}

# locate_tool prints the override path, else the PATH match; it prints nothing when the tool is absent.
locate_tool() {
	local override="$1"
	local name="$2"
	local found
	if [[ -n "$override" ]]; then
		printf '%s\n' "$override"
	elif found="$(command -v "$name")"; then
		printf '%s\n' "$found"
	fi
}

check_actionlint() {
	local actionlint_bin="$1"
	if [[ -z "$actionlint_bin" || ! -x "$actionlint_bin" ]]; then
		printf 'actionlint %s is required; executable is unavailable\n' \
			"$ACTIONLINT_VERSION" >&2
		return 1
	fi
	local actual_actionlint_version
	actual_actionlint_version="$("$actionlint_bin" -version | awk 'NR == 1 { print $1; exit }')"
	if [[ "$actual_actionlint_version" != "$ACTIONLINT_VERSION" ]]; then
		printf 'actionlint version is %s, want %s\n' \
			"${actual_actionlint_version:-unknown}" "$ACTIONLINT_VERSION" >&2
		return 1
	fi
}

check_shellcheck() {
	local shellcheck_bin="$1"
	if [[ -z "$shellcheck_bin" || ! -x "$shellcheck_bin" ]]; then
		printf 'ShellCheck %s is required by actionlint; executable is unavailable\n' \
			"$SHELLCHECK_VERSION" >&2
		return 1
	fi
	local actual_shellcheck_version
	actual_shellcheck_version="$("$shellcheck_bin" --version | awk '$1 == "version:" { print $2; exit }')"
	if [[ "$actual_shellcheck_version" != "$SHELLCHECK_VERSION" ]]; then
		printf 'actionlint ShellCheck version is %s, want %s\n' \
			"${actual_shellcheck_version:-unknown}" "$SHELLCHECK_VERSION" >&2
		return 1
	fi
}

main() {
	local root
	root="$(resolve_root "$@")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null
	if [[ ! -f "$root/.github/actionlint.yaml" ]]; then
		printf 'missing actionlint configuration: %s\n' \
			"$root/.github/actionlint.yaml" >&2
		return 1
	fi

	# shellcheck source=/dev/null
	. "$root/tools/tool-versions.env"
	local actionlint_bin shellcheck_bin
	actionlint_bin="$(locate_tool "${ACTIONLINT_BIN:-}" actionlint)"
	check_actionlint "$actionlint_bin"
	shellcheck_bin="$(locate_tool "${SHELLCHECK_BIN:-}" shellcheck)"
	check_shellcheck "$shellcheck_bin"

	local -a files=()
	collect_files "$root"
	if (( ${#files[@]} == 0 )); then
		printf 'actionlint selected no GitHub or Gitea workflows\n' >&2
		return 1
	fi
	(
		cd "$root"
		"$actionlint_bin" \
			-no-color \
			-config-file .github/actionlint.yaml \
			-shellcheck "$shellcheck_bin" \
			-pyflakes= \
			"${files[@]}"
	)
	printf 'actionlint: %d GitHub and Gitea workflows passed\n' "${#files[@]}"
}

main "$@"
