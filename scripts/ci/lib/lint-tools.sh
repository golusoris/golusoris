# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# Sourced lint-tool checks; callers set strict mode.
# shellcheck shell=bash

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

# Version validation keeps actionlint diagnostics consistent between callers.
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

# Version validation keeps the actionlint ShellCheck dependency pinned.
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
